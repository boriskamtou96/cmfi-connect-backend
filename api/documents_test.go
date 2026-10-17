package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// docxBytes is the smallest Word file: a zip holding word/document.xml.
func docxBytes(t *testing.T) []byte {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	file, err := archive.Create("word/document.xml")
	require.NoError(t, err)
	_, err = file.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`))
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	return buffer.Bytes()
}

func uploadDocument(t *testing.T, router http.Handler, token, name string, data []byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, form.Close())
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/documents/files", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// decodeRaw reads the whole answer (content and meta of a page).
func decodeRaw[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	var body T
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body), response.Body.String())
	return body
}

type documentsPage struct {
	Content []libraryDocumentResponse `json:"content"`
	Meta    MetaResponse              `json:"meta"`
}

func listedDocuments(t *testing.T, router http.Handler, token, query string) documentsPage {
	response := send(t, router, http.MethodGet, token, "/v1/documents"+query, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decodeRaw[documentsPage](t, response)
}

func createLibraryDocument(t *testing.T, router http.Handler, token string, body map[string]any) libraryDocumentResponse {
	created := send(t, router, http.MethodPost, token, "/v1/admin/documents", body)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	document := decode[libraryDocumentResponse](t, created)
	t.Cleanup(func() {
		send(t, router, http.MethodDelete, token, fmt.Sprintf("/v1/admin/documents/%d", document.ID), nil)
	})
	return document
}

func TestAnAdminPublishesADocument(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)

	// a Word file, only through the documents
	require.Equal(t, http.StatusUnsupportedMediaType, uploadFile(t, router, admin.Token, "x.docx", docxBytes(t)).Code)
	require.Equal(t, http.StatusForbidden, uploadDocument(t, router, boris.Token, "x.docx", docxBytes(t)).Code)
	uploaded := uploadDocument(t, router, admin.Token, "Règlement intérieur.docx", docxBytes(t))
	require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
	file := decode[libraryUploadResponse](t, uploaded)
	require.Equal(t, "word", file.Kind)
	require.Equal(t, "Règlement intérieur.docx", file.Name)
	// served as a Word file, not a zip
	served := send(t, router, http.MethodGet, "", file.URL, nil)
	require.Equal(t, http.StatusOK, served.Code)
	require.Equal(t, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", served.Header().Get("Content-Type"))

	document := createLibraryDocument(t, router, admin.Token, map[string]any{
		"title": " Règlement intérieur ", "category": "  Règlements ", "file_id": file.ID, "file_name": file.Name,
	})
	require.Equal(t, "Règlement intérieur", document.Title)
	require.Equal(t, "Règlements", document.Category)
	require.False(t, document.Published)
	require.Nil(t, document.PublishedAt)

	// a draft: not for the members, but for the admins
	for _, found := range listedDocuments(t, router, boris.Token, "").Content {
		require.NotEqual(t, document.ID, found.ID)
	}
	admins := send(t, router, http.MethodGet, admin.Token, "/v1/admin/documents?search=reglement%20interieur", nil)
	require.Equal(t, http.StatusOK, admins.Code)
	require.NotEmpty(t, decodeRaw[documentsPage](t, admins).Content)

	path := fmt.Sprintf("/v1/admin/documents/%d", document.ID)
	published := send(t, router, http.MethodPut, admin.Token, path, map[string]any{
		"title": "Règlement intérieur", "description": "À lire par tous.", "category": "Règlements",
		"file_id": file.ID, "file_name": file.Name, "published": true,
	})
	require.Equal(t, http.StatusOK, published.Code, published.Body.String())
	require.NotNil(t, decode[libraryDocumentResponse](t, published).PublishedAt)

	// found by its words, accents and case aside, and by its category
	page := listedDocuments(t, router, boris.Token, "?search=REGLEMENT%20lire&category=reglements")
	require.Equal(t, int64(1), page.Meta.Total)
	require.Equal(t, document.ID, page.Content[0].ID)
	require.Equal(t, "word", page.Content[0].File.Kind)
	require.Empty(t, listedDocuments(t, router, boris.Token, "?search=reglement&category=Formulaires").Content)

	categories := send(t, router, http.MethodGet, boris.Token, "/v1/documents/categories", nil)
	require.Equal(t, http.StatusOK, categories.Code)
	require.Contains(t, decode[[]libraryCategoryResponse](t, categories), libraryCategoryResponse{Name: "Règlements", Count: 1})

	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, admin.Token, path, nil).Code)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodDelete, admin.Token, path, nil).Code)
	// its file went with it
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodGet, "", file.URL, nil).Code)
}

func TestDocumentsComeAPageAtATime(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)
	word := uniqueName()
	for i := range 3 {
		uploaded := uploadDocument(t, router, admin.Token, "cours.pdf", pdfBytes)
		require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
		file := decode[libraryUploadResponse](t, uploaded)
		require.Equal(t, "pdf", file.Kind)
		createLibraryDocument(t, router, admin.Token, map[string]any{
			"title": fmt.Sprintf("%s cours %d", word, i+1), "file_id": file.ID, "published": true,
		})
	}

	first := listedDocuments(t, router, boris.Token, "?size=2&search="+word)
	require.Equal(t, int64(3), first.Meta.Total)
	require.Len(t, first.Content, 2)
	// the newest first
	require.Equal(t, word+" cours 3", first.Content[0].Title)
	second := listedDocuments(t, router, boris.Token, "?size=2&page=2&search="+word)
	require.Len(t, second.Content, 1)
	require.Equal(t, word+" cours 1", second.Content[0].Title)
	// no name given: the title, with the file's extension
	require.Equal(t, word+" cours 1.pdf", second.Content[0].File.Name)
}

func TestADocumentIsChecked(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)

	// neither a PDF nor a Word file
	require.Equal(t, http.StatusUnsupportedMediaType, uploadDocument(t, router, admin.Token, "photo.png", pngBytes(t, 10, 10, color.White)).Code)
	notWord := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 64)...)
	require.Equal(t, http.StatusUnsupportedMediaType, uploadDocument(t, router, admin.Token, "tableau.xls", notWord).Code)
	require.Equal(t, http.StatusCreated, uploadDocument(t, router, admin.Token, "ancien.doc", notWord).Code)

	photo := upload(t, router, admin.Token, pngBytes(t, 10, 10, color.White))
	require.Equal(t, http.StatusCreated, photo.Code)
	for _, body := range []map[string]any{
		{"title": " ", "file_id": 1},
		{"title": "Sans fichier"},
		// an image is not a document
		{"title": "Une image", "file_id": decode[mediaResponse](t, photo).ID},
	} {
		response := send(t, router, http.MethodPost, admin.Token, "/v1/admin/documents", body)
		require.Equal(t, http.StatusBadRequest, response.Code, "%v: %s", body, response.Body.String())
	}
}
