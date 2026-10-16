package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTheDashboardCountsWithoutNames(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)

	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodGet, boris.Token, "/v1/admin/stats", nil).Code)

	response := send(t, router, http.MethodGet, admin.Token, "/v1/admin/stats", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	stats := decode[adminStatsResponse](t, response)
	require.GreaterOrEqual(t, stats.Members.Total, int64(2))
	require.GreaterOrEqual(t, stats.Members.NewThisWeek, int64(2))
	require.GreaterOrEqual(t, stats.Members.NewThisMonth, stats.Members.NewThisWeek)

	// 30 days by default, today last, the empty days too
	require.Len(t, stats.Days, 30)
	today := time.Now().UTC().Format(time.DateOnly)
	require.Equal(t, today, stats.Days[29].Day)
	require.GreaterOrEqual(t, stats.Days[29].NewMembers, int64(2))
	require.NotEmpty(t, stats.Countries)

	week := send(t, router, http.MethodGet, admin.Token, "/v1/admin/stats?days=7", nil)
	require.Equal(t, http.StatusOK, week.Code)
	require.Len(t, decode[adminStatsResponse](t, week).Days, 7)
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodGet, admin.Token, "/v1/admin/stats?days=3", nil).Code)
}
