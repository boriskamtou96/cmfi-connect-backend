package api

import (
	"context"
	"fmt"
	"image/color"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/push"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/stretchr/testify/require"
)

// fakeSender keeps the pushes instead of sending them; the tokens of [dead]
// are refused like Firebase refuses a removed app.
type fakeSender struct {
	mu       sync.Mutex
	tokens   [][]string
	messages []push.Message
	dead     []string
}

func (f *fakeSender) Send(_ context.Context, tokens []string, message push.Message) (push.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, tokens)
	f.messages = append(f.messages, message)
	result := push.Result{}
	for _, token := range tokens {
		if slices.Contains(f.dead, token) {
			result.Dead = append(result.Dead, token)
		} else {
			result.Sent++
		}
	}
	return result, nil
}

func newPushRouter(t *testing.T, publicURL string) (http.Handler, *fakeSender) {
	server, err := New(testStore, &utils.Config{
		Token: utils.TokenConfig{SecretKey: strings.Repeat("k", 32), Duration: "15m"},
		Push:  utils.PushConfig{PublicURL: publicURL},
	})
	require.NoError(t, err)
	sender := &fakeSender{}
	server.push = sender
	return server.SetupRouter(), sender
}

// livesIn gives [m] a profile in [city] and [assembly] (Cameroon).
func livesIn(t *testing.T, router http.Handler, m member, city, assembly string) {
	// the sign-up made the profile: it gets the place
	response := send(t, router, http.MethodPut, m.Token, "/v1/profiles/", map[string]any{
		"first_name": m.FirstName,
		"country":    "CM",
		"city":       city,
		"church":     "CMCI " + city,
		"assembly":   assembly,
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

func addPhone(t *testing.T, router http.Handler, m member, token string) {
	response := send(t, router, http.MethodPut, m.Token, "/v1/devices",
		map[string]string{"token": token, "platform": "android", "language": "fr"})
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
}

func TestAnAdminSendsANotificationToTheMembersOfACity(t *testing.T) {
	router, sender := newPushRouter(t, "https://api.example.org/")
	admin := staffAdmin(t, router)
	city := uniqueName()
	boris := registerMember(t, router, nil)
	marie := registerMember(t, router, nil)
	paul := registerMember(t, router, nil)
	// typed differently by each: the same city
	livesIn(t, router, boris, city, "Biyem-Assi")
	livesIn(t, router, marie, strings.ToLower(city)+" ", "biyem assi")
	livesIn(t, router, paul, uniqueName(), "Biyem-Assi")
	borisPhone := "token-" + uniqueName()
	addPhone(t, router, boris, borisPhone)

	audience := send(t, router, http.MethodGet, admin.Token,
		"/v1/admin/notifications/audience?country=cm&city="+strings.ToLower(city), nil)
	require.Equal(t, http.StatusOK, audience.Code, audience.Body.String())
	reached := decode[audienceResponse](t, audience)
	require.Equal(t, 2, reached.Recipients)
	require.Equal(t, int32(1), reached.Phones)
	require.Equal(t, []audienceValue{{Value: "Biyem-Assi", Count: 2}}, reached.Assemblies)
	// the cities are listed with the country filter only
	require.Contains(t, reached.Cities, audienceValue{Value: city, Count: 2})

	uploaded := upload(t, router, admin.Token, pngBytes(t, 4, 5, color.White))
	require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
	image := decode[mediaResponse](t, uploaded)

	sent := send(t, router, http.MethodPost, admin.Token, "/v1/admin/notifications", map[string]any{
		"title":       "  Veillée de prière  ",
		"body":        "Vendredi à 21 h, à l'assemblée.",
		"audience":    map[string]string{"country": "CM", "city": city},
		"attachments": []map[string]any{{"media_id": image.ID}},
	})
	require.Equal(t, http.StatusCreated, sent.Code, sent.Body.String())
	notification := decode[adminNotificationResponse](t, sent)
	require.Equal(t, "Veillée de prière", notification.Title)
	require.Equal(t, int32(2), notification.Recipients)
	require.Equal(t, int32(1), notification.Pushed)
	require.Equal(t, city, notification.Audience.City)
	require.Len(t, notification.Attachments, 1)
	require.Equal(t, "image", notification.Attachments[0].Kind)
	require.Equal(t, admin.FirstName+" "+admin.LastName, notification.Author)

	// the push: Boris's phone, the image as an absolute link, what to open
	require.Len(t, sender.tokens, 1)
	require.Equal(t, []string{borisPhone}, sender.tokens[0])
	message := sender.messages[0]
	require.Equal(t, "https://api.example.org"+image.URL, message.ImageURL)
	require.Equal(t, "announcement", message.Data["type"])
	require.Equal(t, fmt.Sprint(notification.ID), message.Data["id"])

	// in the app: Marie has it, unread, then read
	inbox := send(t, router, http.MethodGet, marie.Token, "/v1/notifications?page=1&size=10", nil)
	require.Equal(t, http.StatusOK, inbox.Code, inbox.Body.String())
	mine := decode[myNotificationsResponse](t, inbox)
	require.Equal(t, int32(1), mine.Unread)
	require.Len(t, mine.Items, 1)
	require.Nil(t, mine.Items[0].ReadAt)
	require.Equal(t, "Vendredi à 21 h, à l'assemblée.", mine.Items[0].Body)

	path := fmt.Sprintf("/v1/notifications/%d", notification.ID)
	read := send(t, router, http.MethodPut, marie.Token, path+"/read", nil)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	require.NotNil(t, decode[notificationResponse](t, read).ReadAt)
	inbox = send(t, router, http.MethodGet, marie.Token, "/v1/notifications", nil)
	require.Equal(t, int32(0), decode[myNotificationsResponse](t, inbox).Unread)

	// Paul lives elsewhere: nothing, not even by its number
	inbox = send(t, router, http.MethodGet, paul.Token, "/v1/notifications", nil)
	require.Empty(t, decode[myNotificationsResponse](t, inbox).Items)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodGet, paul.Token, path, nil).Code)

	// the admins see who read it
	history := send(t, router, http.MethodGet, admin.Token, fmt.Sprintf("/v1/admin/notifications/%d", notification.ID), nil)
	require.Equal(t, http.StatusOK, history.Code)
	require.Equal(t, int32(1), decode[adminNotificationResponse](t, history).Read)

}

func TestOnlyAdminsSendNotificationsAndSomeoneMustMatch(t *testing.T) {
	router, _ := newPushRouter(t, "")
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)
	body := map[string]any{"title": "Annonce", "body": "Texte", "audience": map[string]string{"city": uniqueName()}}

	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodPost, boris.Token, "/v1/admin/notifications", body).Code)
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodGet, boris.Token, "/v1/admin/notifications/audience", nil).Code)

	nobody := send(t, router, http.MethodPost, admin.Token, "/v1/admin/notifications", body)
	require.Equal(t, http.StatusBadRequest, nobody.Code)
	require.Equal(t, "NO_RECIPIENTS", errorCode(t, nobody))

	empty := send(t, router, http.MethodPost, admin.Token, "/v1/admin/notifications",
		map[string]any{"title": " ", "body": "Texte"})
	require.Equal(t, http.StatusBadRequest, empty.Code)

	unknownFile := send(t, router, http.MethodPost, admin.Token, "/v1/admin/notifications", map[string]any{
		"title": "Annonce", "body": "Texte", "attachments": []map[string]any{{"media_id": 999_999_999}},
	})
	require.Equal(t, http.StatusBadRequest, unknownFile.Code)
}

func TestAPhoneFollowsTheLastAccountAndDeadPhonesAreForgotten(t *testing.T) {
	router, sender := newPushRouter(t, "")
	admin := staffAdmin(t, router)
	city := uniqueName()
	boris := registerMember(t, router, nil)
	marie := registerMember(t, router, nil)
	livesIn(t, router, boris, city, "")
	livesIn(t, router, marie, city, "")

	shared := "token-" + uniqueName()
	gone := "token-" + uniqueName()
	addPhone(t, router, boris, shared)
	// Marie signs in on Boris's phone: it is hers now
	addPhone(t, router, marie, shared)
	addPhone(t, router, marie, gone)
	sender.dead = []string{gone}

	sent := send(t, router, http.MethodPost, admin.Token, "/v1/admin/notifications",
		map[string]any{"title": "Annonce", "body": "Texte", "audience": map[string]string{"city": city}})
	require.Equal(t, http.StatusCreated, sent.Code, sent.Body.String())
	require.Equal(t, int32(1), decode[adminNotificationResponse](t, sent).Pushed)
	require.ElementsMatch(t, []string{shared, gone}, sender.tokens[0])

	count, err := testStore.CountDevicesOfUsers(context.Background(), []int64{boris.ID, marie.ID})
	require.NoError(t, err)
	require.Equal(t, int32(1), count, "the dead token is forgotten, the shared one is Marie's")

	// signing out: the phone stops receiving
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, marie.Token, "/v1/devices/"+shared, nil).Code)
	count, err = testStore.CountDevicesOfUsers(context.Background(), []int64{marie.ID})
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestTheValuesTypedByHandAreGrouped(t *testing.T) {
	require.Equal(t, "yaounde", fold("  Yaoundé "))
	require.Equal(t, "biyem assi", fold("Biyem-Assi"))
	require.Equal(t, fold("Biyem  assi"), fold("biyem_assi"))

	members := []db.ListAudienceMembersRow{
		{ID: 1, Country: "CM", City: "Yaoundé", Assembly: "Biyem-Assi"},
		{ID: 2, Country: "cm", City: "yaounde", Assembly: "biyem assi"},
		{ID: 3, Country: "CM", City: "Yaoundé", Assembly: "Mvog-Mbi"},
		{ID: 4, Country: "Cameroun", City: "Douala"},
		{ID: 5, Country: "FR", City: "Paris"},
	}
	none := audienceFilter{}.places()
	// an old free text ("Cameroun") is not a country of the list
	require.Equal(t, []audienceValue{{Value: "CM", Count: 3}, {Value: "FR", Count: 1}},
		valuesOf(members, none, placeCountry))
	inYaounde := audienceFilter{City: "YAOUNDE"}.places()
	require.Equal(t, []audienceValue{{Value: "Biyem-Assi", Count: 2}, {Value: "Mvog-Mbi", Count: 1}},
		valuesOf(members, inYaounde, placeAssembly))
	// the cities are listed whatever city is chosen, with the other filters
	inCameroon := audienceFilter{Country: "CM", City: "Paris"}.places()
	require.Equal(t, []audienceValue{{Value: "Yaoundé", Count: 3}}, valuesOf(members, inCameroon, placeCity))
}
