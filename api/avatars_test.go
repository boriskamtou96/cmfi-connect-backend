package api

import (
	"bytes"
	"context"
	"fmt"
	"image/color"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/media"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/stretchr/testify/require"
)

// newMediaTestRouter keeps the files of the test in a folder of its own, to
// see them come and go.
func newMediaTestRouter(t *testing.T) (http.Handler, string) {
	dir := t.TempDir()
	server, err := New(testStore, &utils.Config{
		Token: utils.TokenConfig{SecretKey: strings.Repeat("k", 32), Duration: "15m"},
		Media: utils.MediaConfig{Dir: dir},
	})
	require.NoError(t, err)
	return server.SetupRouter(), dir
}

func putAvatar(t *testing.T, router http.Handler, token string, data []byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "photo.png")
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, form.Close())

	request := httptest.NewRequest(http.MethodPut, "/v1/users/me/avatar", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// uploadAvatar puts a square photo for [m] and gives it.
func uploadAvatar(t *testing.T, router http.Handler, m member, side int, fill color.Color) mediaResponse {
	response := putAvatar(t, router, m.Token, pngBytes(t, side, side, fill))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[mediaResponse](t, response)
}

// avatarFile is where the photo lives on the disk of the server.
func avatarFile(dir string, avatar mediaResponse) string {
	return filepath.Join(dir, strings.TrimPrefix(avatar.URL, "/v1/media/"))
}

func requireFile(t *testing.T, path string, exists bool) {
	t.Helper()
	_, err := os.Stat(path)
	if exists {
		require.NoError(t, err, "the file %s should be there", path)
	} else {
		require.ErrorIs(t, err, os.ErrNotExist, "the file %s should be gone", path)
	}
}

func requireMediaRow(t *testing.T, id int64, exists bool) {
	t.Helper()
	rows, err := testStore.ListMediaByIDs(context.Background(), []int64{id})
	require.NoError(t, err)
	require.Equal(t, exists, len(rows) == 1)
}

func myProfileAvatar(t *testing.T, router http.Handler, m member) *mediaResponse {
	response := get(t, router, m.Token, "/v1/profiles/", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[profileResponse](t, response).Avatar
}

func cardAvatar(t *testing.T, router http.Handler, viewer, m member) *mediaResponse {
	response := get(t, router, viewer.Token, "/v1/users/by-username/"+m.Username, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[memberCard](t, response).Avatar
}

func TestAMemberPutsChangesAndTakesOffTheirPhoto(t *testing.T) {
	router, dir := newMediaTestRouter(t)
	boris := registerMember(t, router, nil)
	marie := registerMember(t, router, nil)
	admin := staffAdmin(t, router)

	// no photo yet: null everywhere
	require.Nil(t, myProfileAvatar(t, router, boris))
	require.Nil(t, cardAvatar(t, router, marie, boris))
	require.Contains(t, get(t, router, marie.Token, "/v1/users/by-username/"+boris.Username, nil).Body.String(), `"avatar":null`)

	first := uploadAvatar(t, router, boris, 256, color.RGBA{R: 255, A: 255})
	require.True(t, strings.HasPrefix(first.URL, "/v1/media/"), first.URL)
	require.True(t, strings.HasSuffix(first.URL, ".jpg"), "kept as a JPEG: %s", first.URL)
	require.EqualValues(t, 256, first.Width)
	require.EqualValues(t, 256, first.Height)
	require.Regexp(t, `^#[0-9A-F]{6}$`, first.Color)
	requireFile(t, avatarFile(dir, first), true)

	// a public link, like the other images
	served := get(t, router, "", first.URL, nil)
	require.Equal(t, http.StatusOK, served.Code)
	require.Equal(t, "image/jpeg", served.Header().Get("Content-Type"))

	// on their profile, their card, and in the back office
	require.Equal(t, &first, myProfileAvatar(t, router, boris))
	require.Equal(t, &first, cardAvatar(t, router, marie, boris))
	adminView := get(t, router, admin.Token, fmt.Sprintf("/v1/admin/members/%d", boris.ID), nil)
	require.Equal(t, http.StatusOK, adminView.Code, adminView.Body.String())
	require.Equal(t, &first, decode[adminMemberResponse](t, adminView).Avatar)
	listed, _ := listedMembers(t, router, admin.Token, "search="+boris.Username)
	require.Len(t, listed, 1)
	require.Equal(t, &first, listed[0].Avatar)
	// the others keep theirs (none)
	require.Nil(t, cardAvatar(t, router, boris, marie))

	// a new photo replaces the first one, whose file goes
	second := uploadAvatar(t, router, boris, 300, color.RGBA{B: 255, A: 255})
	require.NotEqual(t, first.ID, second.ID)
	requireFile(t, avatarFile(dir, first), false)
	requireMediaRow(t, first.ID, false)
	requireFile(t, avatarFile(dir, second), true)
	require.Equal(t, &second, myProfileAvatar(t, router, boris))
	require.Equal(t, &second, cardAvatar(t, router, marie, boris))

	// taken off: null again, the file gone
	removed := send(t, router, http.MethodDelete, boris.Token, "/v1/users/me/avatar", nil)
	require.Equal(t, http.StatusNoContent, removed.Code, removed.Body.String())
	requireFile(t, avatarFile(dir, second), false)
	requireMediaRow(t, second.ID, false)
	require.Nil(t, myProfileAvatar(t, router, boris))
	require.Nil(t, cardAvatar(t, router, marie, boris))
	require.Equal(t, http.StatusNotFound, get(t, router, "", second.URL, nil).Code)

	// nothing left to take off
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, boris.Token, "/v1/users/me/avatar", nil).Code)
}

func TestAProfilePhotoIsASquareJPEGOrPNG(t *testing.T) {
	router, dir := newMediaTestRouter(t)
	boris := registerMember(t, router, nil)
	blue := color.RGBA{B: 255, A: 255}

	for name, data := range map[string][]byte{
		"not square": pngBytes(t, 300, 200, blue),
		"too small":  pngBytes(t, 64, 64, blue),
	} {
		response := putAvatar(t, router, boris.Token, data)
		require.Equal(t, http.StatusBadRequest, response.Code, name)
		require.Equal(t, "AVATAR_INVALID", errorCode(t, response), name)
	}

	for name, data := range map[string][]byte{
		"a PDF":      pdfBytes,
		"text":       []byte("not a picture"),
		"broken PNG": pngBytes(t, 200, 200, blue)[:60],
	} {
		response := putAvatar(t, router, boris.Token, data)
		require.Equal(t, http.StatusUnsupportedMediaType, response.Code, name)
		require.Equal(t, "MEDIA_UNSUPPORTED", errorCode(t, response), name)
	}

	// just over the limit, and far over it (the body is cut while read)
	for _, size := range []int{media.MaxAvatarSize + 1, 3 * media.MaxAvatarSize} {
		response := putAvatar(t, router, boris.Token, bytes.Repeat([]byte{0xFF}, size))
		require.Equal(t, http.StatusRequestEntityTooLarge, response.Code, "%d bytes", size)
		require.Equal(t, "MEDIA_TOO_LARGE", errorCode(t, response))
	}

	// without a file, or without an account
	noFile := send(t, router, http.MethodPut, boris.Token, "/v1/users/me/avatar", map[string]string{})
	require.Equal(t, http.StatusBadRequest, noFile.Code)
	require.Equal(t, http.StatusUnauthorized, putAvatar(t, router, "", pngBytes(t, 200, 200, blue)).Code)

	// nothing was kept
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, files)
	require.Nil(t, myProfileAvatar(t, router, boris))
}

func TestAnAdminTakesOffAMemberPhoto(t *testing.T) {
	router, dir := newMediaTestRouter(t)
	boris := registerMember(t, router, nil)
	marie := registerMember(t, router, nil)
	admin := staffAdmin(t, router)
	photo := uploadAvatar(t, router, boris, 200, color.RGBA{G: 255, A: 255})
	path := fmt.Sprintf("/v1/admin/members/%d/avatar", boris.ID)

	// a member can't
	forbidden := send(t, router, http.MethodDelete, marie.Token, path, nil)
	require.Equal(t, http.StatusForbidden, forbidden.Code)
	requireFile(t, avatarFile(dir, photo), true)

	removed := send(t, router, http.MethodDelete, admin.Token, path, nil)
	require.Equal(t, http.StatusNoContent, removed.Code, removed.Body.String())
	requireFile(t, avatarFile(dir, photo), false)
	requireMediaRow(t, photo.ID, false)
	require.Nil(t, cardAvatar(t, router, marie, boris))

	// again: nothing to take off
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, admin.Token, path, nil).Code)

	unknown := send(t, router, http.MethodDelete, admin.Token, "/v1/admin/members/999999999999/avatar", nil)
	require.Equal(t, http.StatusNotFound, unknown.Code)
	require.Equal(t, "NOT_FOUND", errorCode(t, unknown))
}

func TestThePhotoGoesWithTheAccount(t *testing.T) {
	router, dir := newMediaTestRouter(t)

	// the member deletes their account
	boris := registerMember(t, router, nil)
	photo := uploadAvatar(t, router, boris, 200, color.RGBA{R: 255, G: 255, A: 255})
	response := deleteAccount(t, router, boris.Token, "Secret123")
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	requireFile(t, avatarFile(dir, photo), false)
	requireMediaRow(t, photo.ID, false)

	// through DELETE /v1/users/:id/
	marie := registerMember(t, router, nil)
	photo = uploadAvatar(t, router, marie, 200, color.RGBA{R: 255, B: 255, A: 255})
	deleted := send(t, router, http.MethodDelete, marie.Token, fmt.Sprintf("/v1/users/%d/", marie.ID), nil)
	require.Equal(t, http.StatusNoContent, deleted.Code, deleted.Body.String())
	requireFile(t, avatarFile(dir, photo), false)
	requireMediaRow(t, photo.ID, false)
}
