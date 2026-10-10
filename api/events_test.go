package api

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// pngBytes is a small image of one color.
func pngBytes(t *testing.T, width, height int, fill color.Color) []byte {
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			picture.Set(x, y, fill)
		}
	}
	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, picture))
	return buffer.Bytes()
}

func upload(t *testing.T, router http.Handler, token string, data []byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "poster.png")
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

// staffMember signs up a member and makes them an admin.
func staffAdmin(t *testing.T, router http.Handler) member {
	admin := registerMember(t, router, nil)
	setRole(t, admin.ID, "admin")
	return admin
}

// createEvent makes an event through the API; [fields] override the defaults.
func createEvent(t *testing.T, router http.Handler, token string, fields map[string]any) eventResponse {
	start := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	body := map[string]any{
		"title":     "Conférence annuelle " + uniqueName(),
		"theme":     "Marcher dans l'Esprit",
		"starts_at": start,
		"ends_at":   start.Add(48 * time.Hour),
		"city":      "Yaoundé",
		"country":   "Cameroun",
		"status":    "published",
	}
	for key, value := range fields {
		body[key] = value
	}
	response := send(t, router, http.MethodPost, token, "/v1/admin/events", body)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	event := decode[eventResponse](t, response)
	t.Cleanup(func() { _, _ = testStore.DeleteEvent(context.Background(), event.ID) })
	return event
}

func eventPath(event eventResponse, suffix string) string {
	return fmt.Sprintf("/v1/events/%d%s", event.ID, suffix)
}

func adminEventPath(event eventResponse, suffix string) string {
	return fmt.Sprintf("/v1/admin/events/%d%s", event.ID, suffix)
}

func TestAnAdminPublishesAnEventWithItsPosterAndProgram(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	member := registerMember(t, router, nil)

	uploaded := upload(t, router, admin.Token, pngBytes(t, 40, 50, color.RGBA{R: 30, G: 106, B: 85, A: 255}))
	require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
	poster := decode[mediaResponse](t, uploaded)
	require.Equal(t, int32(40), poster.Width)
	require.Equal(t, int32(50), poster.Height)
	require.Equal(t, "#1E6A55", poster.Color)

	event := createEvent(t, router, admin.Token, map[string]any{"poster_id": poster.ID, "asks_lodging": true})
	require.Equal(t, poster.URL, event.Poster.URL)
	require.Equal(t, "open", event.Registration.State)

	start := event.StartsAt
	program := send(t, router, http.MethodPut, admin.Token, adminEventPath(event, "/sessions"), map[string]any{
		"sessions": []map[string]any{
			{"starts_at": start.Add(2 * time.Hour), "title": "Culte d'ouverture", "speaker": "Pasteur A.",
				"hymns": []map[string]any{{"book": "rendons-ministere-1", "number": 1}}},
			{"starts_at": start, "title": "Accueil", "place": "Hall"},
		},
	})
	require.Equal(t, http.StatusOK, program.Code, program.Body.String())
	sessions := decode[eventResponse](t, program).Sessions
	require.Len(t, sessions, 2)
	require.Equal(t, "Accueil", sessions[0].Title)
	require.JSONEq(t, `[{"book":"rendons-ministere-1","number":1}]`, string(sessions[1].Hymns))

	// a member finds it among the upcoming events, and its poster is public
	upcoming := decode[[]eventSummaryResponse](t, get(t, router, member.Token, "/v1/events/?when=upcoming&size=50", nil))
	var found *eventSummaryResponse
	for i := range upcoming {
		if upcoming[i].ID == event.ID {
			found = &upcoming[i]
		}
	}
	require.NotNil(t, found)
	require.Equal(t, poster.URL, found.Poster.URL)

	image := get(t, router, "", poster.URL, nil)
	require.Equal(t, http.StatusOK, image.Code)
	require.Equal(t, "image/png", image.Header().Get("Content-Type"))
	require.Contains(t, image.Header().Get("Cache-Control"), "immutable")
	require.Equal(t, http.StatusNotFound, get(t, router, "", "/v1/media/../../etc.png", nil).Code)
}

func TestOnlyAdminsCreateEventsAndDraftsStayHidden(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	member := registerMember(t, router, nil)

	refused := send(t, router, http.MethodPost, member.Token, "/v1/admin/events", map[string]any{"title": "X"})
	require.Equal(t, http.StatusForbidden, refused.Code)
	require.Equal(t, http.StatusForbidden, upload(t, router, member.Token, pngBytes(t, 2, 2, color.White)).Code)

	draft := createEvent(t, router, admin.Token, map[string]any{"status": "draft"})
	require.Equal(t, http.StatusNotFound, get(t, router, member.Token, eventPath(draft, ""), nil).Code)
	require.Equal(t, http.StatusOK, get(t, router, admin.Token, eventPath(draft, ""), nil).Code)
	for _, event := range decode[[]eventSummaryResponse](t, get(t, router, member.Token, "/v1/events/?size=50", nil)) {
		require.NotEqual(t, draft.ID, event.ID)
	}

	// what can't be read is refused
	bad := send(t, router, http.MethodPost, admin.Token, "/v1/admin/events", map[string]any{
		"title": "X", "starts_at": time.Now(), "ends_at": time.Now().Add(-time.Hour),
	})
	require.Equal(t, http.StatusBadRequest, bad.Code)
	require.Equal(t, http.StatusUnsupportedMediaType, upload(t, router, admin.Token, []byte("not an image")).Code)
}

func TestMembersRegisterWithinThePlacesLeft(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)
	marc := registerMember(t, router, nil)
	event := createEvent(t, router, admin.Token, map[string]any{
		"capacity": 3, "asks_lodging": true, "asks_departure_city": true,
	})

	registered := send(t, router, http.MethodPut, boris.Token, eventPath(event, "/registration"), map[string]any{
		"people": 2, "needs_lodging": true, "departure_city": " Douala ",
	})
	require.Equal(t, http.StatusOK, registered.Code, registered.Body.String())
	mine := decode[eventResponse](t, registered)
	require.True(t, mine.Registration.Registered)
	require.Equal(t, int32(2), mine.Registration.People)
	require.Equal(t, "Douala", mine.MyRegistration.DepartureCity)

	// 2 + 2 > 3
	full := send(t, router, http.MethodPut, marc.Token, eventPath(event, "/registration"), map[string]any{"people": 2})
	require.Equal(t, http.StatusConflict, full.Code)
	require.Equal(t, "EVENT_FULL", errorCode(t, full))
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPut, marc.Token, eventPath(event, "/registration"), map[string]any{"people": 1}).Code)

	// the last place is taken: the event is full for the others
	detail := decode[eventResponse](t, get(t, router, admin.Token, eventPath(event, ""), nil))
	require.Equal(t, "full", detail.Registration.State)

	// the organizers see who comes, beds and cities
	list := get(t, router, admin.Token, adminEventPath(event, "/registrations"), nil)
	require.Equal(t, http.StatusOK, list.Code)
	totals := decode[registrationsResponse](t, list)
	require.Equal(t, int32(3), totals.People)
	require.Equal(t, int32(2), totals.Lodging)
	require.Equal(t, []cityCount{{City: "Douala", People: 2}}, totals.Cities)
	require.Equal(t, http.StatusForbidden, get(t, router, boris.Token, adminEventPath(event, "/registrations"), nil).Code)

	// Boris doesn't come any more
	left := send(t, router, http.MethodDelete, boris.Token, eventPath(event, "/registration"), nil)
	require.Equal(t, http.StatusOK, left.Code)
	require.Nil(t, decode[eventResponse](t, left).MyRegistration)
}

func TestNoRegistrationWhenClosedOrOver(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	member := registerMember(t, router, nil)

	closed := createEvent(t, router, admin.Token, map[string]any{"registration_open": false})
	refused := send(t, router, http.MethodPut, member.Token, eventPath(closed, "/registration"), map[string]any{})
	require.Equal(t, http.StatusConflict, refused.Code)
	require.Equal(t, "REGISTRATION_CLOSED", errorCode(t, refused))

	past := time.Now().Add(-72 * time.Hour)
	over := createEvent(t, router, admin.Token, map[string]any{"starts_at": past, "ends_at": past.Add(24 * time.Hour)})
	require.Equal(t, "ended", over.Registration.State)
	require.Equal(t, http.StatusConflict, send(t, router, http.MethodPut, member.Token, eventPath(over, "/registration"), map[string]any{}).Code)

	pastList := decode[[]eventSummaryResponse](t, get(t, router, member.Token, "/v1/events/?when=past&size=50", nil))
	ids := []int64{}
	for _, event := range pastList {
		ids = append(ids, event.ID)
	}
	require.Contains(t, ids, over.ID)
}

func TestTheNextEditionIsPreparedFromThisOne(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	event := createEvent(t, router, admin.Token, map[string]any{"time_zone": "Africa/Douala"})
	send(t, router, http.MethodPut, admin.Token, adminEventPath(event, "/sessions"), map[string]any{
		"sessions": []map[string]any{{"starts_at": event.StartsAt, "title": "Ouverture"}},
	})

	duplicated := send(t, router, http.MethodPost, admin.Token, adminEventPath(event, "/duplicate"), nil)
	require.Equal(t, http.StatusCreated, duplicated.Code, duplicated.Body.String())
	next := decode[eventResponse](t, duplicated)
	t.Cleanup(func() { _, _ = testStore.DeleteEvent(context.Background(), next.ID) })

	require.Equal(t, "draft", next.Status)
	require.Equal(t, event.StartsAt.AddDate(1, 0, 0).Unix(), next.StartsAt.Unix())
	require.Len(t, next.Sessions, 1)
	require.NotNil(t, next.SeriesID)

	// once published, each edition lists the other one
	update := map[string]any{"title": next.Title, "starts_at": next.StartsAt, "ends_at": next.EndsAt, "status": "published"}
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPut, admin.Token, adminEventPath(next, ""), update).Code)
	this := decode[eventResponse](t, get(t, router, admin.Token, eventPath(event, ""), nil))
	require.Equal(t, next.SeriesID, this.SeriesID)
	require.Len(t, this.Editions, 1)
	require.Equal(t, next.ID, this.Editions[0].ID)
}

func TestNewsAndPhotosOfAnEvent(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	member := registerMember(t, router, nil)
	event := createEvent(t, router, admin.Token, nil)
	photo := decode[mediaResponse](t, upload(t, router, admin.Token, pngBytes(t, 8, 6, color.Black)))

	send(t, router, http.MethodPost, admin.Token, adminEventPath(event, "/posts"), map[string]any{"body": "Inscriptions ouvertes", "pinned": true})
	posted := send(t, router, http.MethodPost, admin.Token, adminEventPath(event, "/posts"), map[string]any{"body": "La salle change", "image_id": photo.ID})
	require.Equal(t, http.StatusCreated, posted.Code, posted.Body.String())
	posts := decode[eventResponse](t, posted).Posts
	// pinned first, then the latest
	require.Equal(t, "Inscriptions ouvertes", posts[0].Body)
	require.Equal(t, photo.URL, posts[1].Image.URL)

	gallery := send(t, router, http.MethodPost, admin.Token, adminEventPath(event, "/photos"), map[string]any{"media_ids": []int64{photo.ID}})
	require.Equal(t, http.StatusCreated, gallery.Code, gallery.Body.String())
	photos := decode[eventResponse](t, gallery).Photos
	require.Len(t, photos, 1)

	// members read them; only admins remove them
	seen := decode[eventResponse](t, get(t, router, member.Token, eventPath(event, ""), nil))
	require.Len(t, seen.Posts, 2)
	require.Len(t, seen.Photos, 1)
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodDelete, member.Token, adminEventPath(event, fmt.Sprintf("/photos/%d", photos[0].ID)), nil).Code)
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, admin.Token, adminEventPath(event, fmt.Sprintf("/photos/%d", photos[0].ID)), nil).Code)
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, admin.Token, adminEventPath(event, fmt.Sprintf("/posts/%d", posts[1].ID)), nil).Code)
}
