package api

import (
	"image/color"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/stretchr/testify/require"
)

// With a CDN (MEDIA_PUBLIC_URL), the links of the images are its own, and the
// links given before it lead there.
func TestImagesOnTheCDN(t *testing.T) {
	dir := t.TempDir()
	server, err := New(testStore, &utils.Config{
		Token: utils.TokenConfig{SecretKey: strings.Repeat("k", 32), Duration: "15m"},
		Media: utils.MediaConfig{Dir: dir, PublicURL: "https://media.example.org/"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { mediaPrefix = apiMediaPrefix })
	router := server.SetupRouter()
	boris := registerMember(t, router, nil)

	avatar := uploadAvatar(t, router, boris, 200, color.RGBA{G: 200, A: 255})
	require.True(t, strings.HasPrefix(avatar.URL, "https://media.example.org/"), avatar.URL)
	key := strings.TrimPrefix(avatar.URL, "https://media.example.org/")
	requireFile(t, filepath.Join(dir, key), true)
	require.Equal(t, avatar.URL, myProfileAvatar(t, router, boris).URL)

	// an old link of the API: sent to the CDN for good
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/media/"+key, nil))
	require.Equal(t, http.StatusMovedPermanently, response.Code)
	require.Equal(t, avatar.URL, response.Header().Get("Location"))

	// not a file name: not sent anywhere
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/media/..%2F.env", nil))
	require.Equal(t, http.StatusNotFound, response.Code)
}

// Without a CDN, the API serves the images itself.
func TestImagesServedByTheAPI(t *testing.T) {
	router, _ := newMediaTestRouter(t)
	boris := registerMember(t, router, nil)

	avatar := uploadAvatar(t, router, boris, 200, color.RGBA{B: 200, A: 255})
	require.True(t, strings.HasPrefix(avatar.URL, "/v1/media/"), avatar.URL)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, avatar.URL, nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "image/jpeg", response.Header().Get("Content-Type"))
	require.Contains(t, response.Header().Get("Cache-Control"), "immutable")
}
