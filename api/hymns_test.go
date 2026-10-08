package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/hymns"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/go-faker/faker/v4"
	"github.com/stretchr/testify/require"
)

// newHymnsTestServer returns a router, a valid access token and a book imported for the test.
func newHymnsTestServer(t *testing.T) (http.Handler, string, hymns.File) {
	server, err := New(testStore, &utils.Config{Token: utils.TokenConfig{SecretKey: strings.Repeat("k", 32)}})
	require.NoError(t, err)

	token, err := server.jwtAuthenticator.GenerateToken(1, "+237690000001", time.Minute)
	require.NoError(t, err)

	file := hymns.File{
		Book: hymns.Book{Code: "test-" + strings.ToLower(faker.Username()), Title: "Recueil de test", Language: "fr"},
		Hymns: []hymns.Hymn{
			{Number: 1, Title: "A Toi la gloire", Author: "EMPEYTAZ.", Parts: []hymns.Part{
				{Kind: hymns.KindVerse, Label: "1", Lines: []string{"À toi la gloire", "Ô Ressuscité"}},
				{Kind: hymns.KindChorus, Repeat: 2, Lines: []string{"À toi la victoire"}},
			}},
			{Number: 2, Title: "Saint, Saint, Saint", Parts: []hymns.Part{
				{Kind: hymns.KindVerse, Label: "1", Lines: []string{"Saint ! Saint ! Saint ! est l'Eternel"}},
			}},
		},
	}
	result, err := testStore.ImportHymnBookTx(context.Background(), file, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = testStore.DeleteHymnBook(context.Background(), result.Book.ID) })

	return server.SetupRouter(), token, file
}

func get(t *testing.T, router http.Handler, token, path string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

type bookBody struct {
	Content struct {
		Code      string         `json:"code"`
		HymnCount int64          `json:"hymn_count"`
		Version   string         `json:"version"`
		Hymns     []hymnResponse `json:"hymns"`
	} `json:"content"`
}

func TestGetHymnBookReturnsTheWholeBook(t *testing.T) {
	router, token, file := newHymnsTestServer(t)

	response := get(t, router, token, "/v1/hymns/books/"+file.Book.Code, nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.NotEmpty(t, response.Header().Get("ETag"))

	var body bookBody
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, file.Book.Code, body.Content.Code)
	require.Equal(t, int64(2), body.Content.HymnCount)
	require.NotEmpty(t, body.Content.Version)
	require.Len(t, body.Content.Hymns, 2)
	require.Equal(t, "À toi la gloire", body.Content.Hymns[0].FirstLine)
	require.Equal(t, "EMPEYTAZ.", body.Content.Hymns[0].Author)
	require.Equal(t, hymns.KindChorus, body.Content.Hymns[0].Parts[1].Kind)
	require.Equal(t, int32(2), body.Content.Hymns[0].Parts[1].Repeat)
}

func TestGetHymnBookAnswers304WhenTheAppIsUpToDate(t *testing.T) {
	router, token, file := newHymnsTestServer(t)
	path := "/v1/hymns/books/" + file.Book.Code

	etag := get(t, router, token, path, nil).Header().Get("ETag")

	response := get(t, router, token, path, map[string]string{"If-None-Match": etag})
	require.Equal(t, http.StatusNotModified, response.Code)
	require.Empty(t, response.Body.Bytes())

	// an old version gets the book again
	response = get(t, router, token, path, map[string]string{"If-None-Match": `"old"`})
	require.Equal(t, http.StatusOK, response.Code)
}

func TestListHymnBooksIncludesTheVersion(t *testing.T) {
	router, token, file := newHymnsTestServer(t)

	response := get(t, router, token, "/v1/hymns/books", nil)
	require.Equal(t, http.StatusOK, response.Code)

	var body struct {
		Content []hymnBookResponse `json:"content"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	for _, book := range body.Content {
		if book.Code == file.Book.Code {
			require.Equal(t, int64(2), book.HymnCount)
			require.NotEmpty(t, book.Version)
			return
		}
	}
	t.Fatalf("book %s not listed", file.Book.Code)
}

func TestGetHymn(t *testing.T) {
	router, token, file := newHymnsTestServer(t)

	response := get(t, router, token, "/v1/hymns/books/"+file.Book.Code+"/2", nil)
	require.Equal(t, http.StatusOK, response.Code)
	var body struct {
		Content hymnResponse `json:"content"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "Saint, Saint, Saint", body.Content.Title)

	require.Equal(t, http.StatusNotFound, get(t, router, token, "/v1/hymns/books/"+file.Book.Code+"/99", nil).Code)
	require.Equal(t, http.StatusNotFound, get(t, router, token, "/v1/hymns/books/unknown-book", nil).Code)
	require.Equal(t, http.StatusBadRequest, get(t, router, token, "/v1/hymns/books/"+file.Book.Code+"/zero", nil).Code)
}

func TestHymnsNeedAToken(t *testing.T) {
	router, _, _ := newHymnsTestServer(t)

	require.Equal(t, http.StatusUnauthorized, get(t, router, "", "/v1/hymns/books", nil).Code)
}

func TestEtagMatches(t *testing.T) {
	require.True(t, etagMatches(`"a@1"`, `"a@1"`))
	require.True(t, etagMatches(`W/"a@1"`, `"a@1"`))
	require.True(t, etagMatches(`"x", "a@1"`, `"a@1"`))
	require.True(t, etagMatches(`*`, `"a@1"`))
	require.False(t, etagMatches(``, `"a@1"`))
	require.False(t, etagMatches(`"a@0"`, `"a@1"`))
}
