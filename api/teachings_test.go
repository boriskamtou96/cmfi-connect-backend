package api

import (
	"bytes"
	"fmt"
	"image/color"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// mp3Bytes: an ID3 tag, then a frame header (MPEG 1, layer III).
var mp3Bytes = append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), 0xFF, 0xFB, 0x90, 0x64, 0, 0, 0, 0)

// m4aBytes: the MP4 box "ftyp" of an audio file.
var m4aBytes = append([]byte{0, 0, 0, 0x20}, []byte("ftypM4A \x00\x00\x00\x00M4A mp42isom\x00\x00\x00\x00")...)

func uploadAudio(t *testing.T, router http.Handler, token, name string, data []byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, form.Close())
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/teachings/files", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

type teachingsPage struct {
	Content []teachingResponse `json:"content"`
	Meta    MetaResponse       `json:"meta"`
}

func listedTeachings(t *testing.T, router http.Handler, token, query string) teachingsPage {
	response := send(t, router, http.MethodGet, token, "/v1/teachings"+query, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decodeRaw[teachingsPage](t, response)
}

func createTeachingFor(t *testing.T, router http.Handler, token string, body map[string]any) teachingResponse {
	created := send(t, router, http.MethodPost, token, "/v1/admin/teachings", body)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	teaching := decode[teachingResponse](t, created)
	t.Cleanup(func() {
		send(t, router, http.MethodDelete, token, fmt.Sprintf("/v1/admin/teachings/%d", teaching.ID), nil)
	})
	return teaching
}

func TestAnAdminPublishesATeaching(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)

	// a sound, only through the teachings, only by an admin
	require.Equal(t, http.StatusUnsupportedMediaType, uploadFile(t, router, admin.Token, "x.mp3", mp3Bytes).Code)
	require.Equal(t, http.StatusForbidden, uploadAudio(t, router, boris.Token, "x.mp3", mp3Bytes).Code)
	uploaded := uploadAudio(t, router, admin.Token, "La croix.mp3", mp3Bytes)
	require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
	audio := decode[teachingAudioResponse](t, uploaded)
	require.Equal(t, "mp3", audio.Format)
	// served as a sound, a part at a time for the players
	served := send(t, router, http.MethodGet, "", audio.URL, nil)
	require.Equal(t, http.StatusOK, served.Code)
	require.Equal(t, "audio/mpeg", served.Header().Get("Content-Type"))
	require.Equal(t, "bytes", served.Header().Get("Accept-Ranges"))

	cover := upload(t, router, admin.Token, pngBytes(t, 10, 10, color.White))
	require.Equal(t, http.StatusCreated, cover.Code)
	coverID := decode[mediaResponse](t, cover).ID

	teaching := createTeachingFor(t, router, admin.Token, map[string]any{
		"title": " Le message de la croix ", "speaker": " Pr  Zacharias Tanee Fomum ", "category": " Prédications ",
		"recorded_on": "1994-08-15", "audio_id": audio.ID, "duration_seconds": 3125, "cover_id": coverID,
	})
	require.Equal(t, "Le message de la croix", teaching.Title)
	require.Equal(t, "Pr Zacharias Tanee Fomum", teaching.Speaker)
	require.Equal(t, "Prédications", teaching.Category)
	require.Equal(t, "1994-08-15", *teaching.RecordedOn)
	require.Equal(t, int32(3125), teaching.DurationSeconds)
	require.NotNil(t, teaching.Cover)
	require.False(t, teaching.Published)

	// a draft: not for the members, but for the admins
	for _, found := range listedTeachings(t, router, boris.Token, "").Content {
		require.NotEqual(t, teaching.ID, found.ID)
	}
	admins := send(t, router, http.MethodGet, admin.Token, "/v1/admin/teachings?search=croix", nil)
	require.Equal(t, http.StatusOK, admins.Code)
	require.NotEmpty(t, decodeRaw[teachingsPage](t, admins).Content)

	path := fmt.Sprintf("/v1/admin/teachings/%d", teaching.ID)
	published := send(t, router, http.MethodPut, admin.Token, path, map[string]any{
		"title": "Le message de la croix", "speaker": "Pr Zacharias Tanee Fomum", "category": "Prédications",
		"recorded_on": "1994-08-15", "audio_id": audio.ID, "duration_seconds": 3125, "published": true,
	})
	require.Equal(t, http.StatusOK, published.Code, published.Body.String())
	saved := decode[teachingResponse](t, published)
	require.NotNil(t, saved.PublishedAt)
	// the cover taken off: gone with its file
	require.Nil(t, saved.Cover)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodGet, "", teaching.Cover.URL, nil).Code)

	// found by the speaker's name, accents and case aside, and by category
	page := listedTeachings(t, router, boris.Token, "?search=FOMUM%20croix&category=predications")
	require.Equal(t, int64(1), page.Meta.Total)
	require.Equal(t, teaching.ID, page.Content[0].ID)
	require.Empty(t, listedTeachings(t, router, boris.Token, "?search=fomum&category=Chants").Content)

	categories := send(t, router, http.MethodGet, boris.Token, "/v1/teachings/categories", nil)
	require.Equal(t, http.StatusOK, categories.Code)
	require.Contains(t, decode[[]teachingCategoryResponse](t, categories), teachingCategoryResponse{Name: "Prédications", Count: 1})

	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, admin.Token, path, nil).Code)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodDelete, admin.Token, path, nil).Code)
	// its sound went with it
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodGet, "", audio.URL, nil).Code)
}

func TestTeachingsComeAPageAtATime(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)
	word := uniqueName()
	for i, day := range []string{"2020-01-05", "", "2023-06-01"} {
		uploaded := uploadAudio(t, router, admin.Token, "message.m4a", m4aBytes)
		require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
		audio := decode[teachingAudioResponse](t, uploaded)
		require.Equal(t, "m4a", audio.Format)
		createTeachingFor(t, router, admin.Token, map[string]any{
			"title": fmt.Sprintf("%s message %d", word, i+1), "recorded_on": day, "audio_id": audio.ID, "published": true,
		})
	}

	first := listedTeachings(t, router, boris.Token, "?size=2&search="+word)
	require.Equal(t, int64(3), first.Meta.Total)
	require.Len(t, first.Content, 2)
	// the newest message first, those without a day last
	require.Equal(t, word+" message 3", first.Content[0].Title)
	require.Equal(t, word+" message 1", first.Content[1].Title)
	second := listedTeachings(t, router, boris.Token, "?size=2&page=2&search="+word)
	require.Len(t, second.Content, 1)
	require.Equal(t, word+" message 2", second.Content[0].Title)
	require.Nil(t, second.Content[0].RecordedOn)
}

func TestATeachingIsChecked(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)

	// neither an MP3 nor an M4A: a picture, a PDF, a video
	video := append([]byte{0, 0, 0, 0x20}, []byte("ftypisom\x00\x00\x02\x00isomiso2avc1mp41")...)
	for name, data := range map[string][]byte{
		"photo.png": pngBytes(t, 10, 10, color.White), "cours.pdf": pdfBytes, "film.mp4": video,
	} {
		require.Equal(t, http.StatusUnsupportedMediaType, uploadAudio(t, router, admin.Token, name, data).Code, name)
	}
	// the same video named .m4a is taken for a sound
	require.Equal(t, http.StatusCreated, uploadAudio(t, router, admin.Token, "son.m4a", video).Code)

	audio := decode[teachingAudioResponse](t, uploadAudio(t, router, admin.Token, "x.mp3", mp3Bytes))
	photo := decode[mediaResponse](t, upload(t, router, admin.Token, pngBytes(t, 10, 10, color.White)))
	for _, body := range []map[string]any{
		{"title": " ", "audio_id": audio.ID},
		{"title": "Sans son"},
		// a picture is not a sound, a sound is not a cover
		{"title": "Une image", "audio_id": photo.ID},
		{"title": "Couverture", "audio_id": audio.ID, "cover_id": audio.ID},
		{"title": "Demain", "audio_id": audio.ID, "recorded_on": "2999-01-01"},
		{"title": "Date", "audio_id": audio.ID, "recorded_on": "15/08/1994"},
		{"title": "Durée", "audio_id": audio.ID, "duration_seconds": -1},
	} {
		response := send(t, router, http.MethodPost, admin.Token, "/v1/admin/teachings", body)
		require.Equal(t, http.StatusBadRequest, response.Code, "%v: %s", body, response.Body.String())
	}
}
