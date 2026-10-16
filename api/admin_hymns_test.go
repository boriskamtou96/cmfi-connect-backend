package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-faker/faker/v4"
	"github.com/stretchr/testify/require"
)

func adminBookVersion(t *testing.T, router http.Handler, token, code string) string {
	response := send(t, router, http.MethodGet, token, "/v1/admin/hymns/books", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	for _, book := range decode[[]adminHymnBookResponse](t, response) {
		if book.Code == code {
			return book.Version
		}
	}
	t.Fatalf("book %s not listed", code)
	return ""
}

func TestAnAdminAddsCorrectsAndRemovesAHymn(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)
	code := "test-" + strings.ToLower(faker.Username())

	// a member can't make a book
	book := map[string]any{"code": code, "title": "Recueil de test", "language": "fr"}
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodPost, boris.Token, "/v1/admin/hymns/books", book).Code)
	created := send(t, router, http.MethodPost, admin.Token, "/v1/admin/hymns/books", book)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	t.Cleanup(func() {
		if row, err := testStore.GetHymnBookByCode(context.Background(), code); err == nil {
			_ = testStore.DeleteHymnBook(context.Background(), row.ID)
		}
	})
	require.Equal(t, http.StatusConflict, send(t, router, http.MethodPost, admin.Token, "/v1/admin/hymns/books", book).Code)
	empty := adminBookVersion(t, router, admin.Token, code)

	// a new hymn: the book gets a new version, the members read it
	path := fmt.Sprintf("/v1/admin/hymns/books/%s/hymns/216", code)
	hymn := map[string]any{
		"title":  " Mon Dieu, plus près de toi ",
		"author": "S. F. ADAMS.",
		"parts": []map[string]any{
			{"kind": "verse", "label": "1", "lines": []string{"Mon Dieu, plus près de toi,", " Plus près de toi ! "}},
			{"kind": "chorus", "lines": []string{"Plus près de toi !"}},
		},
	}
	added := send(t, router, http.MethodPut, admin.Token, path, hymn)
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	require.Equal(t, "Mon Dieu, plus près de toi", decode[hymnResponse](t, added).Title)
	first := adminBookVersion(t, router, admin.Token, code)
	require.NotEqual(t, empty, first)

	read := send(t, router, http.MethodGet, boris.Token, fmt.Sprintf("/v1/hymns/books/%s/216", code), nil)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	require.Equal(t, []string{"Mon Dieu, plus près de toi,", "Plus près de toi !"}, decode[hymnResponse](t, read).Parts[0].Lines)

	// the same again: nothing changes, the apps don't download it again
	same := send(t, router, http.MethodPut, admin.Token, path, hymn)
	require.Equal(t, http.StatusOK, same.Code, same.Body.String())
	require.Equal(t, first, adminBookVersion(t, router, admin.Token, code))

	// corrected: a new version
	hymn["title"] = "Plus près de toi, mon Dieu"
	corrected := send(t, router, http.MethodPut, admin.Token, path, hymn)
	require.Equal(t, http.StatusOK, corrected.Code, corrected.Body.String())
	require.NotEqual(t, first, adminBookVersion(t, router, admin.Token, code))

	// a hymn without title, without verse or with an empty line is refused
	for _, wrong := range []map[string]any{
		{"title": " ", "parts": hymn["parts"]},
		{"title": "Sans couplet", "parts": []map[string]any{}},
		{"title": "Ligne vide", "parts": []map[string]any{{"kind": "verse", "lines": []string{"Une ligne", " "}}}},
		{"title": "Genre", "parts": []map[string]any{{"kind": "bridge", "lines": []string{"Une ligne"}}}},
	} {
		response := send(t, router, http.MethodPut, admin.Token, path, wrong)
		require.Equal(t, http.StatusBadRequest, response.Code, wrong["title"])
	}

	// renamed and moved; the code stays
	renamed := send(t, router, http.MethodPut, admin.Token, "/v1/admin/hymns/books/"+code,
		map[string]any{"title": "Recueil renommé", "language": "fr", "position": 99})
	require.Equal(t, http.StatusOK, renamed.Code, renamed.Body.String())
	require.Equal(t, "Recueil renommé", decode[adminHymnBookResponse](t, renamed).Title)
	require.Equal(t, int32(99), decode[adminHymnBookResponse](t, renamed).Position)
	require.Equal(t, int64(1), decode[adminHymnBookResponse](t, renamed).HymnCount)

	// removed
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, admin.Token, path, nil).Code)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodDelete, admin.Token, path, nil).Code)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodGet, boris.Token, fmt.Sprintf("/v1/hymns/books/%s/216", code), nil).Code)
}

func TestABookCodeIsPlain(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	for _, code := range []string{"", "Christ Est Vainqueur", "chants_1", "-debut"} {
		response := send(t, router, http.MethodPost, admin.Token, "/v1/admin/hymns/books",
			map[string]any{"code": code, "title": "Recueil", "language": "fr"})
		require.Equal(t, http.StatusBadRequest, response.Code, code)
	}
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPut, admin.Token,
		"/v1/admin/hymns/books/inconnu-0/hymns/1", map[string]any{"title": "x"}).Code)
}
