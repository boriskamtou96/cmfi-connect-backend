package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/stretchr/testify/require"
)

// chooseDiscipleMaker: [disciple] asks [maker] by pseudo, who accepts.
func chooseDiscipleMaker(t *testing.T, router http.Handler, disciple, maker member) authorityResponse {
	created := send(t, router, http.MethodPost, disciple.Token, "/v1/authorities/",
		map[string]any{"username": maker.Username, "is_disciple_maker": true})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	authority := decode[authorityResponse](t, created)
	accepted := send(t, router, http.MethodPost, maker.Token, fmt.Sprintf("/v1/authorities/requests/%d/accept", authority.ID), nil)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	return authority
}

// daysAgo is the server's date n days ago, as the reports API writes it.
func daysAgo(n int) string {
	return today().AddDate(0, 0, -n).Format(reportDateLayout)
}

func ptr[T any](value T) *T {
	return &value
}

func saveDay(t *testing.T, router http.Handler, token, date string, body map[string]any) *httptest.ResponseRecorder {
	return send(t, router, http.MethodPut, token, "/v1/reports/"+date, body)
}

func listMyDisciples(t *testing.T, router http.Handler, token, query string) []discipleResponse {
	response := get(t, router, token, "/v1/disciples"+query, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	disciples := decode[[]discipleResponse](t, response)
	require.NotNil(t, disciples, "no disciples is [], not null")
	return disciples
}

func TestADiscipleMakerSeesTheRegularityOfTheirDisciples(t *testing.T) {
	router := newTestRouter(t)
	maker := registerMember(t, router, nil)
	zoe := registerMember(t, router, map[string]string{"first_name": "Zoe" + uniqueName()})
	abel := registerMember(t, router, map[string]string{"first_name": "Abel" + uniqueName()})

	// Zoe asks: nothing shows until the maker accepts
	asked := send(t, router, http.MethodPost, zoe.Token, "/v1/authorities/",
		map[string]any{"username": maker.Username, "is_disciple_maker": true})
	require.Equal(t, http.StatusCreated, asked.Code, asked.Body.String())
	authority := decode[authorityResponse](t, asked)
	require.True(t, authority.SharesRegularity, "shared by default")
	require.Empty(t, listMyDisciples(t, router, maker.Token, ""))
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPost, maker.Token,
		fmt.Sprintf("/v1/authorities/requests/%d/accept", authority.ID), nil).Code)
	require.True(t, listMyAuthorities(t, router, zoe.Token)[0].SharesRegularity)

	// yesterday, 2 and 3 days ago, a gap, 5 days ago, and 40 days ago (out of the 30)
	for n, goal := range map[int]any{1: 80, 2: 75, 3: nil, 5: 40, 40: 10} {
		body := map[string]any{"note": "fidèle"}
		if goal != nil {
			body["goal_percent"] = goal
		}
		saved := saveDay(t, router, zoe.Token, daysAgo(n), body)
		require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
	}
	// Abel filled in a day long ago only
	require.Equal(t, http.StatusOK, saveDay(t, router, abel.Token, daysAgo(40), map[string]any{"goal_percent": 90}).Code)
	chooseDiscipleMaker(t, router, abel, maker)

	disciples := listMyDisciples(t, router, maker.Token, "?today="+daysAgo(0))
	require.Len(t, disciples, 2)
	// by first name
	require.Equal(t, abel.Username, disciples[0].Member.Username)
	require.Equal(t, discipleRegularity{Days: []string{}, LastDay: ptr(daysAgo(40))}, *disciples[0].Regularity)

	got := disciples[1]
	require.Equal(t, authority.ID, got.AuthorityID)
	require.Equal(t, zoe.Username, got.Member.Username)
	require.Equal(t, zoe.FirstName, got.Member.FirstName)
	require.True(t, got.SharesRegularity)
	// today isn't filled in yet: the series counts from yesterday, the gap stops it;
	// the goals: (80 + 75 + 40) / 3, the day without goals and the old one left out
	before := discipleRegularity{
		Streak:       3,
		Days:         []string{daysAgo(1), daysAgo(2), daysAgo(3), daysAgo(5)},
		FilledLast30: 4,
		LastDay:      ptr(daysAgo(1)),
		GoalPercent:  ptr(65),
	}
	require.Equal(t, before, *got.Regularity)
	// without the phone's date: the server's
	require.Equal(t, before, *listMyDisciples(t, router, maker.Token, "")[1].Regularity)

	// today filled in: (100 + 80 + 75 + 40) / 4 = 73.75
	require.Equal(t, http.StatusOK, saveDay(t, router, zoe.Token, daysAgo(0), map[string]any{"goal_percent": 100}).Code)
	after := discipleRegularity{
		Streak:       4,
		Days:         []string{daysAgo(0), daysAgo(1), daysAgo(2), daysAgo(3), daysAgo(5)},
		FilledLast30: 5,
		LastDay:      ptr(daysAgo(0)),
		GoalPercent:  ptr(74),
	}
	require.Equal(t, after, *listMyDisciples(t, router, maker.Token, "?today="+daysAgo(0))[1].Regularity)
	// a phone a day behind sees its own today; one far off gets the server's
	require.Equal(t, before, *listMyDisciples(t, router, maker.Token, "?today="+daysAgo(1))[1].Regularity)
	require.Equal(t, after, *listMyDisciples(t, router, maker.Token, "?today=2020-01-01")[1].Regularity)
	require.Equal(t, http.StatusBadRequest, get(t, router, maker.Token, "/v1/disciples?today=14-10-2026", nil).Code)

	// an older app saves today again without goals: the day has none any more
	require.Equal(t, http.StatusOK, saveDay(t, router, zoe.Token, daysAgo(0), map[string]any{"note": "sans objectifs"}).Code)
	regularity := listMyDisciples(t, router, maker.Token, "")[1].Regularity
	require.Equal(t, 65, *regularity.GoalPercent)
	require.Equal(t, 4, regularity.Streak)

	// the gap filled: the series runs back to 5 days ago
	require.Equal(t, http.StatusOK, saveDay(t, router, zoe.Token, daysAgo(4), map[string]any{}).Code)
	require.Equal(t, 6, listMyDisciples(t, router, maker.Token, "")[1].Regularity.Streak)

	// Zoe has no disciples of her own
	require.Empty(t, listMyDisciples(t, router, zoe.Token, ""))
}

func TestOnlyAcceptedDiscipleMakerLinksAreListed(t *testing.T) {
	router := newTestRouter(t)
	maker := registerMember(t, router, nil)
	pending := registerMember(t, router, nil)
	notMaker := registerMember(t, router, nil)
	declined := registerMember(t, router, nil)

	require.Equal(t, http.StatusCreated, send(t, router, http.MethodPost, pending.Token, "/v1/authorities/",
		map[string]any{"username": maker.Username, "is_disciple_maker": true}).Code)

	plain := decode[authorityResponse](t, send(t, router, http.MethodPost, notMaker.Token, "/v1/authorities/",
		map[string]any{"username": maker.Username, "is_disciple_maker": false}))
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPost, maker.Token,
		fmt.Sprintf("/v1/authorities/requests/%d/accept", plain.ID), nil).Code)

	refused := decode[authorityResponse](t, send(t, router, http.MethodPost, declined.Token, "/v1/authorities/",
		map[string]any{"username": maker.Username, "is_disciple_maker": true}))
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPost, maker.Token,
		fmt.Sprintf("/v1/authorities/requests/%d/decline", refused.ID), nil).Code)

	for _, someone := range []member{pending, notMaker, declined} {
		require.Equal(t, http.StatusOK, saveDay(t, router, someone.Token, daysAgo(0), map[string]any{}).Code)
	}

	response := get(t, router, maker.Token, "/v1/disciples", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"content":[]`)
	require.Equal(t, http.StatusUnauthorized, get(t, router, "", "/v1/disciples", nil).Code)
}

func TestADiscipleChoosesWhetherToShareTheirRegularity(t *testing.T) {
	router := newTestRouter(t)
	disciple := registerMember(t, router, nil)
	maker := registerMember(t, router, nil)
	other := registerMember(t, router, nil)
	authority := chooseDiscipleMaker(t, router, disciple, maker)
	require.Equal(t, http.StatusOK, saveDay(t, router, disciple.Token, daysAgo(1), map[string]any{"goal_percent": 50}).Code)

	// only the disciple who owns the link can change it
	path := fmt.Sprintf("/v1/authorities/%d/sharing", authority.ID)
	off := map[string]any{"shares": false}
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPut, other.Token, path, off).Code)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPut, maker.Token, path, off).Code)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPut, disciple.Token,
		fmt.Sprintf("/v1/authorities/%d/sharing", authority.ID+1_000_000), off).Code)
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPut, disciple.Token, path, map[string]any{}).Code)
	require.True(t, listMyDisciples(t, router, maker.Token, "")[0].SharesRegularity)

	turnedOff := send(t, router, http.MethodPut, disciple.Token, path, off)
	require.Equal(t, http.StatusOK, turnedOff.Code, turnedOff.Body.String())
	updated := decode[authorityResponse](t, turnedOff)
	require.Equal(t, authority.ID, updated.ID)
	require.Equal(t, maker.Username, *updated.Username)
	require.Equal(t, "accepted", updated.Status)
	require.False(t, updated.SharesRegularity)
	require.False(t, decode[authorityResponse](t, get(t, router, disciple.Token, fmt.Sprintf("/v1/authorities/%d", authority.ID), nil)).SharesRegularity)
	require.False(t, listMyAuthorities(t, router, disciple.Token)[0].SharesRegularity)

	// the maker still sees the disciple, nothing of their reports
	response := get(t, router, maker.Token, "/v1/disciples", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"regularity":null`)
	disciples := decode[[]discipleResponse](t, response)
	require.Len(t, disciples, 1)
	require.Equal(t, disciple.Username, disciples[0].Member.Username)
	require.False(t, disciples[0].SharesRegularity)
	require.Nil(t, disciples[0].Regularity)

	// turned back on
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPut, disciple.Token, path, map[string]any{"shares": true}).Code)
	regularity := listMyDisciples(t, router, maker.Token, "")[0].Regularity
	require.NotNil(t, regularity)
	require.Equal(t, 1, regularity.Streak)
	require.Equal(t, 50, *regularity.GoalPercent)
}

func TestTheGoalsReachedOnADay(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)
	date := daysAgo(0)
	stored := func() *int16 {
		report, err := testStore.GetDailyReport(context.Background(),
			db.GetDailyReportParams{UserID: boris.ID, ReportDate: today()})
		require.NoError(t, err)
		if !report.GoalPercent.Valid {
			return nil
		}
		return &report.GoalPercent.Int16
	}

	for _, wrong := range []any{101, -1, "half"} {
		response := saveDay(t, router, boris.Token, date, map[string]any{"goal_percent": wrong})
		require.Equal(t, http.StatusBadRequest, response.Code, "%v: %s", wrong, response.Body.String())
	}

	require.Equal(t, http.StatusOK, saveDay(t, router, boris.Token, date, map[string]any{"goal_percent": 0}).Code)
	require.Equal(t, int16(0), *stored())
	require.Equal(t, http.StatusOK, saveDay(t, router, boris.Token, date, map[string]any{"goal_percent": 66.6}).Code)
	require.Equal(t, int16(67), *stored())
	require.Equal(t, http.StatusOK, saveDay(t, router, boris.Token, date, map[string]any{"goal_percent": nil}).Code)
	require.Nil(t, stored())
	require.Equal(t, http.StatusOK, saveDay(t, router, boris.Token, date, map[string]any{"goal_percent": 100}).Code)
	require.Equal(t, int16(100), *stored())
	// an app that doesn't send it: no goals that day
	require.Equal(t, http.StatusOK, saveDay(t, router, boris.Token, date, map[string]any{"note": "ok"}).Code)
	require.Nil(t, stored())
}
