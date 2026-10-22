package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSoulsAreKeptForTheirMemberOnly(t *testing.T) {
	router := newTestRouter(t)
	grace := registerMember(t, router, nil)
	other := registerMember(t, router, nil)

	saved := send(t, router, http.MethodPut, grace.Token, "/v1/souls/k3x9", map[string]any{
		"name": " Amina Ngono ", "phone": "+237 6 99 00 00 00", "place": "Marché central",
		"met_on": "2026-10-20", "stage": "interested", "next_contact": "2026-10-25",
		"notes": []map[string]any{{"at": "2026-10-21T09:00:00Z", "text": "Appelée, elle viendra dimanche"}, {"at": "2026-10-21T10:00:00Z", "text": "  "}},
	})
	require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
	soul := decode[soulBody](t, saved)
	require.Equal(t, "Amina Ngono", soul.Name)
	require.Equal(t, "2026-10-25", *soul.NextContact)
	require.Len(t, soul.Notes, 1)

	// changed later: the same soul
	soul.Stage = "received"
	soul.NextContact = nil
	changed := send(t, router, http.MethodPut, grace.Token, "/v1/souls/k3x9", soul)
	require.Equal(t, http.StatusOK, changed.Code, changed.Body.String())

	mine := decode[[]soulBody](t, send(t, router, http.MethodGet, grace.Token, "/v1/souls", nil))
	require.Len(t, mine, 1)
	require.Equal(t, "received", mine[0].Stage)
	require.Nil(t, mine[0].NextContact)

	// nobody else sees them, nor deletes them
	require.Empty(t, decode[[]soulBody](t, send(t, router, http.MethodGet, other.Token, "/v1/souls", nil)))
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, other.Token, "/v1/souls/k3x9", nil).Code)
	require.Len(t, decode[[]soulBody](t, send(t, router, http.MethodGet, grace.Token, "/v1/souls", nil)), 1)

	// deleted, twice is fine
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, grace.Token, "/v1/souls/k3x9", nil).Code)
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, grace.Token, "/v1/souls/k3x9", nil).Code)
	require.Empty(t, decode[[]soulBody](t, send(t, router, http.MethodGet, grace.Token, "/v1/souls", nil)))
}

func TestASoulIsChecked(t *testing.T) {
	router := newTestRouter(t)
	m := registerMember(t, router, nil)
	for _, body := range []map[string]any{
		{"name": "", "met_on": "2026-10-20"},
		{"name": "Paul", "met_on": "20/10/2026"},
		{"name": "Paul", "met_on": "2026-10-20", "stage": "saved"},
	} {
		response := send(t, router, http.MethodPut, m.Token, "/v1/souls/a1", body)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	}
}
