package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func overdueOf(t *testing.T, router http.Handler, token, query string) overdueResponse {
	t.Helper()
	response := get(t, router, token, "/v1/disciples/overdue"+query, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[overdueResponse](t, response)
}

func TestADiscipleMakerSeesWhoIsLateAndRemindsThem(t *testing.T) {
	router := newTestRouter(t)
	maker := registerMember(t, router, nil)
	recent := registerMember(t, router, nil)
	late := registerMember(t, router, nil)
	never := registerMember(t, router, nil)
	hidden := registerMember(t, router, nil)
	for _, disciple := range []member{recent, late, never, hidden} {
		chooseDiscipleMaker(t, router, disciple, maker)
	}
	require.Equal(t, http.StatusOK, saveDay(t, router, recent.Token, daysAgo(1), map[string]any{"note": "ok"}).Code)
	require.Equal(t, http.StatusOK, saveDay(t, router, late.Token, daysAgo(6), map[string]any{"note": "ok"}).Code)
	require.Equal(t, http.StatusOK, saveDay(t, router, hidden.Token, daysAgo(20), map[string]any{"note": "ok"}).Code)
	// hidden turns the sharing off: nothing is known about them
	for _, authority := range listMyAuthorities(t, router, hidden.Token) {
		off := send(t, router, http.MethodPut, hidden.Token, fmt.Sprintf("/v1/authorities/%d/sharing", authority.ID),
			map[string]any{"shares": false})
		require.Equal(t, http.StatusOK, off.Code, off.Body.String())
	}

	overdue := overdueOf(t, router, maker.Token, "")
	require.Equal(t, 3, overdue.Days)
	require.Equal(t, 3, overdue.Sharing)
	require.Equal(t, 1, overdue.NotSharing)
	// the ones who never filled one first, then the longest without
	require.Len(t, overdue.Overdue, 2)
	require.Equal(t, never.Username, overdue.Overdue[0].Member.Username)
	require.Nil(t, overdue.Overdue[0].DaysSince)
	require.Equal(t, late.Username, overdue.Overdue[1].Member.Username)
	require.Equal(t, 6, *overdue.Overdue[1].DaysSince)
	require.Equal(t, daysAgo(6), *overdue.Overdue[1].LastDay)
	require.True(t, overdue.Overdue[1].CanNudge)

	// 10 days: only the one who never filled; 1 day: the recent one too
	require.Len(t, overdueOf(t, router, maker.Token, "?days=10").Overdue, 1)
	require.Len(t, overdueOf(t, router, maker.Token, "?days=1").Overdue, 3)
	require.Equal(t, http.StatusBadRequest, get(t, router, maker.Token, "/v1/disciples/overdue?days=0x", nil).Code)

	// one tap: every late disciple
	sent := send(t, router, http.MethodPost, maker.Token, "/v1/disciples/nudge", map[string]any{})
	require.Equal(t, http.StatusOK, sent.Code, sent.Body.String())
	require.Equal(t, nudgeResponse{Sent: 2}, decode[nudgeResponse](t, sent))

	// once a day: the second tap sends nothing
	again := decode[nudgeResponse](t, send(t, router, http.MethodPost, maker.Token, "/v1/disciples/nudge", map[string]any{}))
	require.Equal(t, nudgeResponse{Skipped: 2}, again)
	after := overdueOf(t, router, maker.Token, "")
	require.False(t, after.Overdue[0].CanNudge)
	require.NotNil(t, after.Overdue[0].LastNudgeAt)
	require.WithinDuration(t, time.Now(), *after.Overdue[0].LastNudgeAt, time.Minute)

	// a disciple by name: only one of the maker's own
	recentLink := overdueOf(t, router, maker.Token, "?days=1").Overdue
	var recentID int64
	for _, item := range recentLink {
		if item.Member.Username == recent.Username {
			recentID = item.AuthorityID
		}
	}
	require.NotZero(t, recentID)
	one := decode[nudgeResponse](t, send(t, router, http.MethodPost, maker.Token, "/v1/disciples/nudge",
		map[string]any{"authority_ids": []int64{recentID}}))
	require.Equal(t, nudgeResponse{Sent: 1}, one)

	// somebody else's disciple: not found, and a member without disciples sees none
	stranger := registerMember(t, router, nil)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPost, stranger.Token, "/v1/disciples/nudge",
		map[string]any{"authority_ids": []int64{recentID}}).Code)
	require.Empty(t, overdueOf(t, router, stranger.Token, "").Overdue)
	require.Equal(t, http.StatusUnauthorized, get(t, router, "", "/v1/disciples/overdue", nil).Code)
}
