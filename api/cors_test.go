package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/stretchr/testify/require"
)

// corsRouter is a router started with [server] (GIN_MODE, CORS_ORIGINS).
func corsRouter(t *testing.T, server utils.ServerConfig) http.Handler {
	s, err := New(testStore, &utils.Config{
		Server: server,
		Token:  utils.TokenConfig{SecretKey: strings.Repeat("k", 32), Duration: "15m"},
	})
	require.NoError(t, err)
	return s.SetupRouter()
}

// preflight: what a browser of [origin] asks before a POST with a token and JSON.
func preflight(router http.Handler, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodOptions, "/v1/admin/notifications", nil)
	request.Header.Set("Origin", origin)
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func fromOrigin(t *testing.T, router http.Handler, origin string) *httptest.ResponseRecorder {
	return get(t, router, "", "/v1/health", map[string]string{"Origin": origin})
}

func TestTheAllowedSitesCallTheAPIFromABrowser(t *testing.T) {
	admin := "https://admin.cmfi-connect.com"
	router := corsRouter(t, utils.ServerConfig{
		GinMode:     "release",
		CORSOrigins: []string{admin, "https://cmfi-connect.com/"},
	})

	response := preflight(router, admin)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	header := response.Header()
	require.Equal(t, admin, header.Get("Access-Control-Allow-Origin"))
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		require.Contains(t, strings.Split(header.Get("Access-Control-Allow-Methods"), ","), method)
	}
	for _, name := range []string{"Origin", "Content-Type", "Accept", "Authorization", "Accept-Language"} {
		require.Contains(t, strings.Split(header.Get("Access-Control-Allow-Headers"), ","), name)
	}
	require.Equal(t, "43200", header.Get("Access-Control-Max-Age"))
	require.Empty(t, header.Get("Access-Control-Allow-Credentials"), "a Bearer token, no cookie")

	// the request itself
	response = fromOrigin(t, router, admin)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, admin, response.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "Content-Length,Content-Disposition,X-Request-Id", response.Header().Get("Access-Control-Expose-Headers"))
	// written with a final / in CORS_ORIGINS
	require.Equal(t, "https://cmfi-connect.com",
		fromOrigin(t, router, "https://cmfi-connect.com").Header().Get("Access-Control-Allow-Origin"))

	// any other site, the local ones too in release
	for _, origin := range []string{"https://evil.example", "http://localhost:5173", "http://admin.cmfi-connect.com",
		"https://admin.cmfi-connect.com.evil.example"} {
		for _, response := range []*httptest.ResponseRecorder{preflight(router, origin), fromOrigin(t, router, origin)} {
			require.Equal(t, http.StatusForbidden, response.Code, origin)
			require.Empty(t, response.Header().Get("Access-Control-Allow-Origin"), origin)
		}
	}

	// the apps send no Origin: not concerned
	response = get(t, router, "", "/v1/health", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
}

func TestWithoutAllowedSitesOnlyLocalOnesInDevelopment(t *testing.T) {
	router := corsRouter(t, utils.ServerConfig{GinMode: "debug"})
	for _, origin := range []string{"http://localhost:5173", "http://127.0.0.1:4173", "http://localhost"} {
		response := preflight(router, origin)
		require.Equal(t, http.StatusNoContent, response.Code, origin)
		require.Equal(t, origin, response.Header().Get("Access-Control-Allow-Origin"))
		require.Equal(t, origin, fromOrigin(t, router, origin).Header().Get("Access-Control-Allow-Origin"))
	}
	for _, origin := range []string{"https://evil.example", "http://localhost.evil.example", "https://localhost:5173",
		"http://evil.example:5173", "null"} {
		response := preflight(router, origin)
		require.Equal(t, http.StatusForbidden, response.Code, origin)
		require.Empty(t, response.Header().Get("Access-Control-Allow-Origin"), origin)
	}

	// in release: no site at all
	router = corsRouter(t, utils.ServerConfig{GinMode: "release"})
	for _, origin := range []string{"http://localhost:5173", "https://admin.cmfi-connect.com"} {
		response := preflight(router, origin)
		require.Equal(t, http.StatusForbidden, response.Code, origin)
		require.Empty(t, response.Header().Get("Access-Control-Allow-Origin"), origin)
	}
	require.Equal(t, http.StatusOK, get(t, router, "", "/v1/health", nil).Code)
}

func TestIsLocalOrigin(t *testing.T) {
	for origin, local := range map[string]bool{
		"http://localhost:5173":          true,
		"http://127.0.0.1:8080":          true,
		"http://localhost":               true,
		"https://localhost:5173":         false,
		"http://localhost:5173/":         false,
		"http://localhost.evil.example":  false,
		"http://evil.example/localhost":  false,
		"http://user@localhost:5173":     false,
		"http://localhost:abc":           false,
		"http://127.0.0.2:5173":          false,
		"localhost:5173":                 false,
		"":                               false,
		"http://localhost@evil.example:": false,
	} {
		require.Equal(t, local, isLocalOrigin(origin), origin)
	}
}
