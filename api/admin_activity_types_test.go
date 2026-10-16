package api

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func memberItems(t *testing.T, router http.Handler, token string) []activityTypeResponse {
	response := send(t, router, http.MethodGet, token, "/v1/activities/", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[[]activityTypeResponse](t, response)
}

func hasItem(items []activityTypeResponse, code string) bool {
	for _, item := range items {
		if item.Code == code {
			return true
		}
	}
	return false
}

func TestAnAdminKeepsTheStandardItems(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)

	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodGet, boris.Token, "/v1/admin/activity-types", nil).Code)
	listed := send(t, router, http.MethodGet, admin.Token, "/v1/admin/activity-types", nil)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var bible adminActivityTypeResponse
	for _, item := range decode[[]adminActivityTypeResponse](t, listed) {
		if item.Code == "LB" {
			bible = item
		}
	}
	require.True(t, bible.Locked)

	// the app counts the Bible reading in LB: it stays
	locked := send(t, router, http.MethodPut, admin.Token, fmt.Sprintf("/v1/admin/activity-types/%d/archive", bible.ID),
		map[string]any{"archived": true})
	require.Equal(t, http.StatusConflict, locked.Code, locked.Body.String())

	// a new standard item reaches every member
	code := fmt.Sprintf("T%06d", rand.IntN(1_000_000))
	created := send(t, router, http.MethodPost, admin.Token, "/v1/admin/activity-types", map[string]any{
		"code": code, "label": "Visites", "tracks_quantity": true, "quantity_unit": "visites",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	item := decode[adminActivityTypeResponse](t, created)
	require.True(t, item.IsStandard)
	require.Less(t, item.Position, int32(100))
	require.True(t, hasItem(memberItems(t, router, boris.Token), code))

	// renamed and moved
	path := fmt.Sprintf("/v1/admin/activity-types/%d", item.ID)
	renamed := send(t, router, http.MethodPut, admin.Token, path,
		map[string]any{"label": "Visites aux malades", "quantity_unit": "personnes", "position": 50})
	require.Equal(t, http.StatusOK, renamed.Code, renamed.Body.String())
	require.Equal(t, "Visites aux malades", decode[adminActivityTypeResponse](t, renamed).Label)
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPut, admin.Token, path,
		map[string]any{"label": "Visites", "position": 150}).Code)

	// archived: no longer offered; restored: back
	archived := send(t, router, http.MethodPut, admin.Token, path+"/archive", map[string]any{"archived": true})
	require.Equal(t, http.StatusOK, archived.Code, archived.Body.String())
	require.True(t, decode[adminActivityTypeResponse](t, archived).Archived)
	require.False(t, hasItem(memberItems(t, router, boris.Token), code))
	restored := send(t, router, http.MethodPut, admin.Token, path+"/archive", map[string]any{"archived": false})
	require.Equal(t, http.StatusOK, restored.Code, restored.Body.String())
	require.True(t, hasItem(memberItems(t, router, boris.Token), code))
	// left archived: the next runs don't see it
	send(t, router, http.MethodPut, admin.Token, path+"/archive", map[string]any{"archived": true})

	// the code of a member's own item can't become standard
	mine := fmt.Sprintf("P%06d", rand.IntN(1_000_000))
	own := send(t, router, http.MethodPost, boris.Token, "/v1/activities/",
		map[string]any{"code": mine, "label": "Mon élément", "tracks_duration": true})
	require.Equal(t, http.StatusCreated, own.Code, own.Body.String())
	taken := send(t, router, http.MethodPost, admin.Token, "/v1/admin/activity-types",
		map[string]any{"code": mine, "label": "Standard", "tracks_duration": true})
	require.Equal(t, http.StatusConflict, taken.Code, taken.Body.String())
}
