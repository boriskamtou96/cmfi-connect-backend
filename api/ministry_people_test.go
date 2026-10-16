package api

import (
	"fmt"
	"image/color"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func listedPeople(t *testing.T, router http.Handler, token, kind string) []ministryPersonResponse {
	response := send(t, router, http.MethodGet, token, "/v1/ministry/people?kind="+kind, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[[]ministryPersonResponse](t, response)
}

func findPerson(people []ministryPersonResponse, id int64) *ministryPersonResponse {
	for _, person := range people {
		if person.ID == id {
			return &person
		}
	}
	return nil
}

func TestAnAdminPublishesAMissionary(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)

	uploaded := upload(t, router, admin.Token, pngBytes(t, 400, 400, color.RGBA{R: 10, G: 90, B: 160, A: 255}))
	require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
	photo := decode[mediaResponse](t, uploaded)

	body := map[string]any{
		"kind": "missionary", "name": " Paul Ndi ", "photo_id": photo.ID, "country_code": "td",
		"role": "Implantation d'églises", "since_year": 2015, "story": "Parti au Tchad en 2015.",
	}
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodPost, boris.Token, "/v1/admin/ministry/people", body).Code)
	created := send(t, router, http.MethodPost, admin.Token, "/v1/admin/ministry/people", body)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	paul := decode[ministryPersonResponse](t, created)
	path := fmt.Sprintf("/v1/admin/ministry/people/%d", paul.ID)
	t.Cleanup(func() { send(t, router, http.MethodDelete, admin.Token, path, nil) })
	require.Equal(t, "Paul Ndi", paul.Name)
	require.Equal(t, "TD", paul.CountryCode)
	require.Equal(t, photo.ID, paul.Photo.ID)
	require.False(t, paul.Published)

	// a draft: the members don't see it yet
	require.Nil(t, findPerson(listedPeople(t, router, boris.Token, "missionary"), paul.ID))

	body["published"] = true
	published := send(t, router, http.MethodPut, admin.Token, path, body)
	require.Equal(t, http.StatusOK, published.Code, published.Body.String())
	seen := findPerson(listedPeople(t, router, boris.Token, "missionary"), paul.ID)
	require.NotNil(t, seen)
	require.Equal(t, int32(2015), *seen.SinceYear)
	require.NotEmpty(t, seen.Photo.URL)
	// not a hero
	require.Nil(t, findPerson(listedPeople(t, router, boris.Token, "hero"), paul.ID))

	// the photo removed
	body["photo_id"] = nil
	removed := send(t, router, http.MethodPut, admin.Token, path, body)
	require.Equal(t, http.StatusOK, removed.Code, removed.Body.String())
	require.Nil(t, decode[ministryPersonResponse](t, removed).Photo)

	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, admin.Token, path, nil).Code)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodDelete, admin.Token, path, nil).Code)
}

func TestAMinistryPersonIsChecked(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	for _, body := range []map[string]any{
		{"kind": "saint", "name": "Quelqu'un"},
		{"kind": "hero", "name": " "},
		{"kind": "hero", "name": "Héros", "country_code": "Cameroun"},
		{"kind": "missionary", "name": "Missionnaire", "since_year": 1800},
		{"kind": "hero", "name": "Héros", "photo_id": 999999999},
	} {
		response := send(t, router, http.MethodPost, admin.Token, "/v1/admin/ministry/people", body)
		require.Equal(t, http.StatusBadRequest, response.Code, body)
	}
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodGet, admin.Token, "/v1/ministry/people?kind=saint", nil).Code)
}
