package api

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// a tiny PDF: enough for the media storage, which only reads its first bytes
var pdfBytes = []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF\n")

func uploadFile(t *testing.T, router http.Handler, token, name string, data []byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, form.Close())

	request := httptest.NewRequest(http.MethodPost, "/v1/admin/media", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func day(offset int) string {
	return todayIn().AddDate(0, 0, offset).Format(prayerDayLayout)
}

// createPrayerTopic makes a topic through the API; [fields] override the defaults.
func createPrayerTopic(t *testing.T, router http.Handler, token string, fields map[string]any) prayerTopicResponse {
	body := map[string]any{
		"title":     "Conférence annuelle " + uniqueName(),
		"category":  "event",
		"starts_on": day(-1),
		"ends_on":   day(10),
		"status":    "published",
		"points":    []string{"Pour les orateurs.", "Pour les participants.", "Pour les voyages."},
	}
	for key, value := range fields {
		body[key] = value
	}
	response := send(t, router, http.MethodPost, token, "/v1/admin/prayer/topics", body)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	topic := decode[prayerTopicResponse](t, response)
	t.Cleanup(func() { _, _ = testStore.DeletePrayerTopic(context.Background(), topic.ID) })
	return topic
}

func currentTopics(t *testing.T, router http.Handler, token string) []prayerTopicResponse {
	response := get(t, router, token, "/v1/prayer/topics?day="+day(0), nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[[]prayerTopicResponse](t, response)
}

func findTopic(topics []prayerTopicResponse, id int64) *prayerTopicResponse {
	for i := range topics {
		if topics[i].ID == id {
			return &topics[i]
		}
	}
	return nil
}

func TestAnAdminPublishesAPrayerTopicMadeFromAPDF(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)
	marie := registerMember(t, router, nil)

	uploaded := uploadFile(t, router, admin.Token, "sujets.pdf", pdfBytes)
	require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
	document := decode[mediaResponse](t, uploaded)
	require.Contains(t, document.URL, ".pdf")

	topic := createPrayerTopic(t, router, admin.Token, map[string]any{
		"document_id":   document.ID,
		"document_name": "sujets-conference-2026.pdf",
		"verse":         "« Priez sans cesse. » 1 Thessaloniciens 5.17",
		"points":        []string{" Pour les orateurs. ", "", "Pour les participants."},
	})
	require.Equal(t, []string{"Pour les orateurs.", "Pour les participants."}, topic.Points)
	require.NotNil(t, topic.Document)
	require.Equal(t, "sujets-conference-2026.pdf", topic.Document.Name)
	require.Equal(t, int64(len(pdfBytes)), topic.Document.SizeBytes)

	// the PDF is served as a PDF
	pdf := get(t, router, "", topic.Document.URL, nil)
	require.Equal(t, http.StatusOK, pdf.Code)
	require.Equal(t, "application/pdf", pdf.Header().Get("Content-Type"))

	seen := findTopic(currentTopics(t, router, boris.Token), topic.ID)
	require.NotNil(t, seen)
	require.Zero(t, seen.People)
	require.Empty(t, seen.PrayedToday)

	// Boris prays for both points, Marie for one: two people pray, nobody knows who
	path := fmt.Sprintf("/v1/prayer/topics/%d/prayed", topic.ID)
	prayed := send(t, router, http.MethodPut, boris.Token, path, map[string]any{"day": day(0), "positions": []int{2, 1, 1}})
	require.Equal(t, http.StatusOK, prayed.Code, prayed.Body.String())
	require.Equal(t, []int32{1, 2}, decode[prayerTopicResponse](t, prayed).PrayedToday)
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPut, marie.Token, path, map[string]any{"day": day(0), "positions": []int{1}}).Code)

	forBoris := findTopic(currentTopics(t, router, boris.Token), topic.ID)
	require.Equal(t, int32(2), forBoris.People)
	require.Equal(t, []int32{1, 2}, forBoris.PrayedToday)
	// unchecking a point
	unchecked := send(t, router, http.MethodPut, boris.Token, path, map[string]any{"day": day(0), "positions": []int{1}})
	require.Equal(t, []int32{1}, decode[prayerTopicResponse](t, unchecked).PrayedToday)

	outOfRange := send(t, router, http.MethodPut, boris.Token, path, map[string]any{"day": day(0), "positions": []int{3}})
	require.Equal(t, http.StatusBadRequest, outOfRange.Code)
}

func TestOnlyAdminsPublishPrayerTopicsAndDraftsStayHidden(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)

	refused := send(t, router, http.MethodPost, boris.Token, "/v1/admin/prayer/topics",
		map[string]any{"title": "Mes sujets", "starts_on": day(0), "points": []string{"Un point"}})
	require.Equal(t, http.StatusForbidden, refused.Code)

	draft := createPrayerTopic(t, router, admin.Token, map[string]any{"status": "draft"})
	require.Nil(t, findTopic(currentTopics(t, router, boris.Token), draft.ID))
	require.Equal(t, http.StatusNotFound, get(t, router, boris.Token, fmt.Sprintf("/v1/prayer/topics/%d", draft.ID), nil).Code)

	// the admins see it, then publish it
	all := decode[[]prayerTopicResponse](t, get(t, router, admin.Token, "/v1/admin/prayer/topics", nil))
	require.NotNil(t, findTopic(all, draft.ID))
	published := send(t, router, http.MethodPut, admin.Token, fmt.Sprintf("/v1/admin/prayer/topics/%d", draft.ID), map[string]any{
		"title": draft.Title, "category": "event", "starts_on": day(0), "status": "published",
		"points": []string{"Premier point corrigé", "Deuxième point"},
	})
	require.Equal(t, http.StatusOK, published.Code, published.Body.String())
	require.Equal(t, []string{"Premier point corrigé", "Deuxième point"}, decode[prayerTopicResponse](t, published).Points)
	require.NotNil(t, findTopic(currentTopics(t, router, boris.Token), draft.ID))

	// the points are optional: the app shows the title and the PDF
	pointless := send(t, router, http.MethodPost, admin.Token, "/v1/admin/prayer/topics",
		map[string]any{"title": "Sans point", "starts_on": day(0), "points": []string{" "}})
	require.Equal(t, http.StatusCreated, pointless.Code, pointless.Body.String())
	require.Empty(t, decode[prayerTopicResponse](t, pointless).Points)
	send(t, router, http.MethodDelete, admin.Token,
		fmt.Sprintf("/v1/admin/prayer/topics/%d", decode[prayerTopicResponse](t, pointless).ID), nil)

	mistakes := []map[string]any{
		{"title": "Catégorie", "starts_on": day(0), "category": "sport", "points": []string{"Un point"}},
		{"title": "Dates", "starts_on": day(2), "ends_on": day(1), "points": []string{"Un point"}},
		{"title": "Pas un PDF", "starts_on": day(0), "document_id": 999999999, "points": []string{"Un point"}},
	}
	for _, body := range mistakes {
		response := send(t, router, http.MethodPost, admin.Token, "/v1/admin/prayer/topics", body)
		require.Equal(t, http.StatusBadRequest, response.Code, body["title"])
	}

	deleted := send(t, router, http.MethodDelete, admin.Token, fmt.Sprintf("/v1/admin/prayer/topics/%d", draft.ID), nil)
	require.Equal(t, http.StatusNoContent, deleted.Code)
}

func TestTheTopicsOfTheDayAndTheArchive(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)

	ended := createPrayerTopic(t, router, admin.Token, map[string]any{"starts_on": day(-20), "ends_on": day(-1)})
	coming := createPrayerTopic(t, router, admin.Token, map[string]any{"starts_on": day(3), "ends_on": nil})
	always := createPrayerTopic(t, router, admin.Token, map[string]any{"starts_on": day(-5), "ends_on": nil})

	current := currentTopics(t, router, boris.Token)
	require.Nil(t, findTopic(current, ended.ID))
	require.Nil(t, findTopic(current, coming.ID))
	require.NotNil(t, findTopic(current, always.ID))

	archive := decode[[]prayerTopicResponse](t, get(t, router, boris.Token, "/v1/prayer/topics?archive=true&day="+day(0), nil))
	require.NotNil(t, findTopic(archive, ended.ID))
}

func TestPrayerListsArePrivate(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)
	marie := registerMember(t, router, nil)

	created := send(t, router, http.MethodPost, boris.Token, "/v1/prayer/people",
		map[string]any{"list": "unbelievers", "name": " Paul M. ", "note": "Collègue"})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	paul := decode[prayerPersonResponse](t, created)
	require.Equal(t, "Paul M.", paul.Name)
	aicha := decode[prayerPersonResponse](t, send(t, router, http.MethodPost, boris.Token, "/v1/prayer/people",
		map[string]any{"list": "unbelievers", "name": "Aïcha N."}))

	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPost, boris.Token, "/v1/prayer/people",
		map[string]any{"list": "friends", "name": "Jean"}).Code)

	// Marie sees nothing of Boris's lists, and can't touch them
	require.Empty(t, decode[[]prayerPersonResponse](t, get(t, router, marie.Token, "/v1/prayer/people", nil)))
	path := fmt.Sprintf("/v1/prayer/people/%d", paul.ID)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPut, marie.Token, path,
		map[string]any{"list": "believers", "name": "Paul"}).Code)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodDelete, marie.Token, path, nil).Code)

	// a retreat prayed for Paul today
	prayed := send(t, router, http.MethodPost, boris.Token, "/v1/prayer/people/prayed", map[string]any{"day": day(0), "ids": []int64{paul.ID}})
	require.Equal(t, http.StatusOK, prayed.Code, prayed.Body.String())
	people := decode[[]prayerPersonResponse](t, prayed)
	require.Len(t, people, 2)
	for _, person := range people {
		if person.ID == paul.ID {
			require.Equal(t, day(0), *person.LastPrayedOn)
		} else {
			require.Nil(t, person.LastPrayedOn)
		}
	}

	// Paul believed in the Lord
	believed := send(t, router, http.MethodPut, boris.Token, path,
		map[string]any{"list": "unbelievers", "name": "Paul M.", "note": "Collègue", "answered_on": day(0)})
	require.Equal(t, http.StatusOK, believed.Code, believed.Body.String())
	require.Equal(t, day(0), *decode[prayerPersonResponse](t, believed).AnsweredOn)

	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, boris.Token, fmt.Sprintf("/v1/prayer/people/%d", aicha.ID), nil).Code)
	require.Len(t, decode[[]prayerPersonResponse](t, get(t, router, boris.Token, "/v1/prayer/people", nil)), 1)
}

func TestTheRetreatsOfTheDay(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)

	noon := send(t, router, http.MethodPut, boris.Token, "/v1/prayer/retreats/"+day(0)+"/noon", nil)
	require.Equal(t, http.StatusOK, noon.Code, noon.Body.String())
	require.Equal(t, int32(15), decode[prayerRetreatResponse](t, noon).Minutes)

	longer := send(t, router, http.MethodPut, boris.Token, "/v1/prayer/retreats/"+day(0)+"/noon", map[string]any{"minutes": 20})
	require.Equal(t, int32(20), decode[prayerRetreatResponse](t, longer).Minutes)
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPut, boris.Token, "/v1/prayer/retreats/"+day(-1)+"/morning", nil).Code)

	retreats := decode[[]prayerRetreatResponse](t, get(t, router, boris.Token,
		fmt.Sprintf("/v1/prayer/retreats?from=%s&to=%s", day(-1), day(0)), nil))
	require.Len(t, retreats, 2)
	require.Equal(t, "morning", retreats[0].Moment)
	require.Equal(t, "noon", retreats[1].Moment)

	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPut, boris.Token, "/v1/prayer/retreats/"+day(0)+"/afternoon", nil).Code)
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, boris.Token, "/v1/prayer/retreats/"+day(0)+"/noon", nil).Code)
	require.Len(t, decode[[]prayerRetreatResponse](t, get(t, router, boris.Token,
		fmt.Sprintf("/v1/prayer/retreats?from=%s&to=%s", day(0), day(0)), nil)), 0)
}

func createImportunity(t *testing.T, router http.Handler, token string, goal int64) importunityDetailResponse {
	response := send(t, router, http.MethodPost, token, "/v1/prayer/importunities",
		map[string]any{"title": "Seigneur, sauve Paul", "description": "Mon collègue de bureau", "goal": goal})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	return decode[importunityDetailResponse](t, response)
}

func importunityPath(importunity importunityDetailResponse, suffix string) string {
	return fmt.Sprintf("/v1/prayer/importunities/%d%s", importunity.ID, suffix)
}

func TestAnImportunityIsPrayedTogetherUntilItsGoal(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)
	marie := registerMember(t, router, nil)
	paul := registerMember(t, router, nil)

	topic := createImportunity(t, router, boris.Token, 100)
	require.True(t, topic.IsOwner)
	require.Equal(t, "joined", topic.MyStatus)
	require.Equal(t, int32(1), topic.Participants)

	invited := send(t, router, http.MethodPost, boris.Token, importunityPath(topic, "/invitations"),
		map[string]any{"usernames": []string{"@" + marie.Username}})
	require.Equal(t, http.StatusOK, invited.Code, invited.Body.String())
	require.Equal(t, int64(1), decode[invitationsResponse](t, invited).Invited)
	unknown := send(t, router, http.MethodPost, boris.Token, importunityPath(topic, "/invitations"),
		map[string]any{"usernames": []string{"personne.inconnue"}})
	require.Equal(t, http.StatusNotFound, unknown.Code)
	require.Equal(t, "USERNAME_NOT_FOUND", errorCode(t, unknown))

	// Marie sees the invitation, from Boris; Paul doesn't see the topic at all
	mine := decode[[]importunityResponse](t, get(t, router, marie.Token, "/v1/prayer/importunities", nil))
	require.Len(t, mine, 1)
	require.Equal(t, "invited", mine[0].MyStatus)
	require.Equal(t, boris.Username, mine[0].InvitedBy.Username)
	require.Equal(t, http.StatusNotFound, get(t, router, paul.Token, importunityPath(topic, ""), nil).Code)

	// invited is not yet praying
	early := send(t, router, http.MethodPost, marie.Token, importunityPath(topic, "/count"), map[string]any{"amount": 5})
	require.Equal(t, http.StatusForbidden, early.Code)
	require.Equal(t, "NOT_PARTICIPANT", errorCode(t, early))

	joined := send(t, router, http.MethodPut, marie.Token, importunityPath(topic, "/answer"), map[string]any{"join": true})
	require.Equal(t, http.StatusOK, joined.Code, joined.Body.String())
	require.Equal(t, int32(2), decode[importunityDetailResponse](t, joined).Participants)

	counted := decode[importunityDetailResponse](t, send(t, router, http.MethodPost, marie.Token, importunityPath(topic, "/count"), map[string]any{"amount": 60}))
	require.Equal(t, int64(60), counted.Total)
	require.Equal(t, int64(60), counted.MyCount)
	require.Nil(t, counted.ReachedAt)

	reached := decode[importunityDetailResponse](t, send(t, router, http.MethodPost, boris.Token, importunityPath(topic, "/count"), map[string]any{"amount": 50}))
	require.Equal(t, int64(110), reached.Total)
	require.Equal(t, int64(50), reached.MyCount)
	require.NotNil(t, reached.ReachedAt)
	// the names of who prays, never their counts
	require.Len(t, reached.Members, 2)
	require.NotContains(t, send(t, router, http.MethodGet, boris.Token, importunityPath(topic, ""), nil).Body.String(), `"count"`)

	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPost, boris.Token, importunityPath(topic, "/count"), map[string]any{"amount": 0}).Code)

	// only the author changes the topic
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodPut, marie.Token, importunityPath(topic, ""),
		map[string]any{"title": "Autre"}).Code)

	// Marie leaves: her proclamations stay; the author can't leave
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, marie.Token, importunityPath(topic, "/membership"), nil).Code)
	afterLeaving := decode[importunityDetailResponse](t, get(t, router, boris.Token, importunityPath(topic, ""), nil))
	require.Equal(t, int64(110), afterLeaving.Total)
	require.Equal(t, int32(1), afterLeaving.Participants)
	ownerLeaves := send(t, router, http.MethodDelete, boris.Token, importunityPath(topic, "/membership"), nil)
	require.Equal(t, "OWNER_CANNOT_LEAVE", errorCode(t, ownerLeaves))

	// gone for Marie; closed, it doesn't count any more
	require.Equal(t, http.StatusNotFound, get(t, router, marie.Token, importunityPath(topic, ""), nil).Code)
	closed := send(t, router, http.MethodPut, boris.Token, importunityPath(topic, ""),
		map[string]any{"title": "Seigneur, sauve Paul", "goal": 100, "status": "closed"})
	require.Equal(t, http.StatusOK, closed.Code, closed.Body.String())
	refused := send(t, router, http.MethodPost, boris.Token, importunityPath(topic, "/count"), map[string]any{"amount": 1})
	require.Equal(t, "IMPORTUNITY_CLOSED", errorCode(t, refused))

	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, boris.Token, importunityPath(topic, ""), nil).Code)
	require.Empty(t, decode[[]importunityResponse](t, get(t, router, boris.Token, "/v1/prayer/importunities", nil)))
}

func TestADeclinedInvitationIsGone(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)
	marie := registerMember(t, router, nil)
	topic := createImportunity(t, router, boris.Token, 0)
	require.Nil(t, topic.Goal)

	send(t, router, http.MethodPost, boris.Token, importunityPath(topic, "/invitations"), map[string]any{"usernames": []string{marie.Username}})
	declined := send(t, router, http.MethodPut, marie.Token, importunityPath(topic, "/answer"), map[string]any{"join": false})
	require.Equal(t, http.StatusNoContent, declined.Code, declined.Body.String())
	require.Empty(t, decode[[]importunityResponse](t, get(t, router, marie.Token, "/v1/prayer/importunities", nil)))
	require.Equal(t, http.StatusNotFound, get(t, router, marie.Token, importunityPath(topic, ""), nil).Code)

	// invited again, the answer stays
	again := send(t, router, http.MethodPost, boris.Token, importunityPath(topic, "/invitations"), map[string]any{"usernames": []string{marie.Username}})
	require.Equal(t, int64(0), decode[invitationsResponse](t, again).Invited)
}

// makeDiscipleOf links [disciple] to [fd] as their disciple maker; [fd] accepts.
func makeDiscipleOf(t *testing.T, router http.Handler, disciple, fd member, accept bool) {
	created := send(t, router, http.MethodPost, disciple.Token, "/v1/authorities/",
		map[string]any{"username": fd.Username, "is_disciple_maker": true})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	if accept {
		authority := decode[authorityResponse](t, created)
		accepted := send(t, router, http.MethodPost, fd.Token, fmt.Sprintf("/v1/authorities/requests/%d/accept", authority.ID), nil)
		require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	}
}

func TestInvitingTheSpiritualFamilyOfTheDiscipleMaker(t *testing.T) {
	router := newTestRouter(t)
	joel := registerMember(t, router, nil)
	boris := registerMember(t, router, nil)
	marie := registerMember(t, router, nil)
	paul := registerMember(t, router, nil)
	alone := registerMember(t, router, nil)

	makeDiscipleOf(t, router, boris, joel, true)
	makeDiscipleOf(t, router, marie, joel, true)
	// Joel hasn't answered Paul yet: he isn't in the family
	makeDiscipleOf(t, router, paul, joel, false)

	family := decode[familyResponse](t, get(t, router, boris.Token, "/v1/prayer/importunities/family", nil))
	require.NotNil(t, family.DiscipleMaker)
	require.Equal(t, joel.Username, family.DiscipleMaker.Username)
	require.Equal(t, 2, family.Members) // Joel and Marie

	noFamily := decode[familyResponse](t, get(t, router, alone.Token, "/v1/prayer/importunities/family", nil))
	require.Nil(t, noFamily.DiscipleMaker)
	require.Zero(t, noFamily.Members)

	topic := createImportunity(t, router, boris.Token, 10_000)
	invited := send(t, router, http.MethodPost, boris.Token, importunityPath(topic, "/invitations"), map[string]any{"family": true})
	require.Equal(t, http.StatusOK, invited.Code, invited.Body.String())
	require.Equal(t, int64(2), decode[invitationsResponse](t, invited).Invited)

	for _, invitee := range []member{joel, marie} {
		mine := decode[[]importunityResponse](t, get(t, router, invitee.Token, "/v1/prayer/importunities", nil))
		require.Len(t, mine, 1, invitee.Username)
		require.Equal(t, "invited", mine[0].MyStatus)
		require.Equal(t, int64(10_000), *mine[0].Goal)
	}
	require.Empty(t, decode[[]importunityResponse](t, get(t, router, paul.Token, "/v1/prayer/importunities", nil)))
	// the author sees how many wait, not who
	detail := decode[importunityDetailResponse](t, get(t, router, boris.Token, importunityPath(topic, ""), nil))
	require.Equal(t, int32(2), detail.Invited)
	require.Len(t, detail.Members, 1)
}
