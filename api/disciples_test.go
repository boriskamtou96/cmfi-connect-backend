package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/stretchr/testify/require"
)

// chooseDiscipleMaker: [disciple] asks [maker] by pseudo, who accepts.
func chooseDiscipleMaker(t *testing.T, router http.Handler, disciple, maker member) authorityResponse {
	return chooseAuthority(t, router, disciple, maker, true)
}

// chooseAuthority: [disciple] asks [authority] by pseudo, as disciple maker or
// not, who accepts.
func chooseAuthority(t *testing.T, router http.Handler, disciple, authority member, discipleMaker bool) authorityResponse {
	created := send(t, router, http.MethodPost, disciple.Token, "/v1/authorities/",
		map[string]any{"username": authority.Username, "is_disciple_maker": discipleMaker})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	asked := decode[authorityResponse](t, created)
	accepted := send(t, router, http.MethodPost, authority.Token, fmt.Sprintf("/v1/authorities/requests/%d/accept", asked.ID), nil)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	return asked
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

type disciplesPage struct {
	Content []discipleResponse `json:"content"`
	Meta    MetaResponse       `json:"meta"`
}

// listDisciplesPage: one page of GET /v1/disciples[query].
func listDisciplesPage(t *testing.T, router http.Handler, token, query string) disciplesPage {
	t.Helper()
	response := get(t, router, token, "/v1/disciples"+query, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page disciplesPage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page), response.Body.String())
	require.NotNil(t, page.Content, "no disciples is [], not null")
	return page
}

// listMyDisciples: the disciples when the first page holds them all.
func listMyDisciples(t *testing.T, router http.Handler, token, query string) []discipleResponse {
	t.Helper()
	page := listDisciplesPage(t, router, token, query)
	require.Equal(t, int64(len(page.Content)), page.Meta.Total, "all on the first page")
	require.Equal(t, MetaResponse{Total: page.Meta.Total, Limit: 20, Offset: 0}, page.Meta)
	return page.Content
}

// usernamesOf: the pseudos of [disciples], in order.
func usernamesOf(disciples []discipleResponse) []string {
	usernames := []string{}
	for _, disciple := range disciples {
		usernames = append(usernames, disciple.Member.Username)
	}
	return usernames
}

func discipleReports(t *testing.T, router http.Handler, token string, authorityID int64, query string) *httptest.ResponseRecorder {
	return get(t, router, token, fmt.Sprintf("/v1/disciples/%d/reports%s", authorityID, query), nil)
}

// myItems: the items [m] can fill in, by code.
func myItems(t *testing.T, router http.Handler, m member) map[string]activityTypeResponse {
	response := get(t, router, m.Token, "/v1/activities/", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	items := map[string]activityTypeResponse{}
	for _, item := range decode[[]activityTypeResponse](t, response) {
		items[item.Code] = item
	}
	return items
}

// percentOf is the share of [total] items filled in on a day, rounded.
func percentOf(filled, total int) float64 {
	return math.Round(float64(filled) * 100 / float64(total))
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
	require.True(t, got.IsDiscipleMaker)
	require.True(t, got.SharesRegularity)
	// today isn't filled in yet: the series counts from yesterday, the gap stops it;
	// the goals: (80 + 75 + 40) / 3, the day without goals and the old one left out;
	// no item filled in on those days
	before := discipleRegularity{
		Streak:       3,
		Days:         []string{daysAgo(1), daysAgo(2), daysAgo(3), daysAgo(5)},
		FilledLast30: 4,
		LastDay:      ptr(daysAgo(1)),
		GoalPercent:  ptr(65),
		ItemsPercent: ptr(0),
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
		ItemsPercent: ptr(0),
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

func TestOnlyAcceptedLinksAreListed(t *testing.T) {
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

	// a member who chose them as an ordinary authority shares their reports too
	response := get(t, router, maker.Token, "/v1/disciples", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"is_disciple_maker":false`)
	require.Contains(t, response.Body.String(), `"meta":{"total":1,"limit":20,"offset":0}`)
	disciples := decode[[]discipleResponse](t, response)
	require.Len(t, disciples, 1)
	require.Equal(t, plain.ID, disciples[0].AuthorityID)
	require.Equal(t, notMaker.Username, disciples[0].Member.Username)
	require.False(t, disciples[0].IsDiscipleMaker)
	require.True(t, disciples[0].SharesRegularity)
	require.Equal(t, []string{daysAgo(0)}, disciples[0].Regularity.Days)
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
	require.Contains(t, response.Body.String(), `"meta":{"total":1,"limit":20,"offset":0}`)
	disciples := decode[[]discipleResponse](t, response)
	require.Len(t, disciples, 1)
	require.Equal(t, disciple.Username, disciples[0].Member.Username)
	require.False(t, disciples[0].SharesRegularity)
	require.Nil(t, disciples[0].Regularity)
	refused := discipleReports(t, router, maker.Token, authority.ID, "")
	require.Equal(t, http.StatusForbidden, refused.Code, refused.Body.String())
	require.Equal(t, "REPORTS_NOT_SHARED", errorCode(t, refused))
	require.NotContains(t, refused.Body.String(), daysAgo(1))

	// turned back on
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPut, disciple.Token, path, map[string]any{"shares": true}).Code)
	regularity := listMyDisciples(t, router, maker.Token, "")[0].Regularity
	require.NotNil(t, regularity)
	require.Equal(t, 1, regularity.Streak)
	require.Equal(t, 50, *regularity.GoalPercent)
	shared := discipleReports(t, router, maker.Token, authority.ID, "")
	require.Equal(t, http.StatusOK, shared.Code, shared.Body.String())
	require.Len(t, decode[discipleReportsResponse](t, shared).Days, 1)
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

func TestEveryAuthorityChosenSeesTheMemberAfterTheDisciples(t *testing.T) {
	router := newTestRouter(t)
	me := registerMember(t, router, nil)
	zoe := registerMember(t, router, map[string]string{"first_name": "Zoe" + uniqueName()})
	abel := registerMember(t, router, map[string]string{"first_name": "Abel" + uniqueName()})
	yves := registerMember(t, router, map[string]string{"first_name": "Yves" + uniqueName()})
	chooseAuthority(t, router, yves, me, false)
	chooseAuthority(t, router, abel, me, false)
	chooseDiscipleMaker(t, router, zoe, me)

	// Abel: 2 items yesterday, 1 two days ago, none 3 days ago, and a day out of the 30
	items := myItems(t, router, abel)
	total := len(items)
	lb, ps := items["LB"], items["PS"]
	for n, entries := range map[int][]map[string]any{
		1:  {{"activity_type_id": lb.ID, "quantity": 3}, {"activity_type_id": ps.ID, "duration_minutes": 20}},
		2:  {{"activity_type_id": lb.ID, "quantity": 1}},
		3:  {},
		31: {{"activity_type_id": lb.ID, "quantity": 2}},
	} {
		saved := saveDay(t, router, abel.Token, daysAgo(n), map[string]any{"note": "privé", "entries": entries})
		require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
	}

	disciples := listMyDisciples(t, router, me.Token, "")
	require.Len(t, disciples, 3)
	// the disciple first, though Abel and Yves come before her by name
	require.Equal(t, zoe.Username, disciples[0].Member.Username)
	require.True(t, disciples[0].IsDiscipleMaker)
	require.Nil(t, disciples[0].Regularity.ItemsPercent, "no report day")
	require.Equal(t, abel.Username, disciples[1].Member.Username)
	require.False(t, disciples[1].IsDiscipleMaker)
	require.Equal(t, yves.Username, disciples[2].Member.Username)
	require.False(t, disciples[2].IsDiscipleMaker)

	// the mean of each day's share, the day out of the 30 left out
	regularity := disciples[1].Regularity
	require.Equal(t, 3, regularity.FilledLast30)
	want := int(math.Round((percentOf(2, total) + percentOf(1, total) + percentOf(0, total)) / 3))
	require.Equal(t, want, *regularity.ItemsPercent)
	require.Nil(t, regularity.GoalPercent)
}

func TestAnAuthoritySeesTheReportsButNeverTheNotes(t *testing.T) {
	router := newTestRouter(t)
	me := registerMember(t, router, nil)
	zoe := registerMember(t, router, nil)
	authority := chooseAuthority(t, router, zoe, me, false)

	// a personal item on top of the standard ones
	created := send(t, router, http.MethodPost, zoe.Token, "/v1/activities/",
		map[string]any{"code": "VEILLE", "label": "Veille", "tracks_duration": true})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	items := myItems(t, router, zoe)
	total := len(items)
	lb, ps, veille := items["LB"], items["PS"], items["VEILLE"]

	require.Equal(t, http.StatusOK, saveDay(t, router, zoe.Token, daysAgo(0), map[string]any{
		"note": "secret du jour", "goal_percent": 75,
		"entries": []map[string]any{
			{"activity_type_id": veille.ID, "duration_minutes": 30},
			{"activity_type_id": lb.ID, "quantity": 3},
		},
	}).Code)
	// a day with only a note
	require.Equal(t, http.StatusOK, saveDay(t, router, zoe.Token, daysAgo(2), map[string]any{"note": "malade"}).Code)
	require.Equal(t, http.StatusOK, saveDay(t, router, zoe.Token, daysAgo(5), map[string]any{
		"goal_percent": 0, "entries": []map[string]any{{"activity_type_id": ps.ID, "duration_minutes": 15}},
	}).Code)

	response := discipleReports(t, router, me.Token, authority.ID, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	raw := response.Body.String()
	require.NotContains(t, raw, `"note"`)
	require.NotContains(t, raw, "secret du jour")
	require.NotContains(t, raw, "malade")

	got := decode[discipleReportsResponse](t, response)
	require.Equal(t, authority.ID, got.AuthorityID)
	require.Equal(t, zoe.Username, got.Member.Username)
	require.Equal(t, zoe.FirstName, got.Member.FirstName)
	require.False(t, got.IsDiscipleMaker)
	require.Equal(t, total, got.TotalItems)
	require.Len(t, got.Days, 3)

	// newest first, the items in the order of the day form
	require.Equal(t, discipleReportDay{
		Date:         daysAgo(0),
		GoalPercent:  ptr(75),
		Filled:       2,
		Total:        total,
		ItemsPercent: int(percentOf(2, total)),
		Entries: []dailyReportHistoryEntry{
			{ActivityTypeID: lb.ID, Code: "LB", Label: lb.Label, QuantityUnit: lb.QuantityUnit, Quantity: ptr(int32(3))},
			{ActivityTypeID: veille.ID, Code: "VEILLE", Label: "Veille", DurationMinutes: ptr(int32(30))},
		},
	}, got.Days[0])
	require.Equal(t, discipleReportDay{
		Date: daysAgo(2), Total: total, Entries: []dailyReportHistoryEntry{},
	}, got.Days[1])
	require.Contains(t, raw, `"entries":[]`)
	require.Equal(t, daysAgo(5), got.Days[2].Date)
	require.Equal(t, 0, *got.Days[2].GoalPercent)
	require.Equal(t, 1, got.Days[2].Filled)
	require.Equal(t, int(percentOf(1, total)), got.Days[2].ItemsPercent)

	// the same entries as the member's own history
	own := decode[[]dailyReportHistoryDay](t, get(t, router, zoe.Token, "/v1/reports/", nil))
	require.Equal(t, own[0].Entries, got.Days[0].Entries)

	// an item archived: no longer counted, its past values still shown
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, zoe.Token,
		fmt.Sprintf("/v1/activities/%d", veille.ID), nil).Code)
	got = decode[discipleReportsResponse](t, discipleReports(t, router, me.Token, authority.ID, ""))
	require.Equal(t, total-1, got.TotalItems)
	require.Equal(t, total-1, got.Days[0].Total)
	require.Equal(t, 2, got.Days[0].Filled)
	require.Len(t, got.Days[0].Entries, 2)

	// a member without any report
	quiet := registerMember(t, router, nil)
	link := chooseDiscipleMaker(t, router, quiet, me)
	empty := discipleReports(t, router, me.Token, link.ID, "")
	require.Equal(t, http.StatusOK, empty.Code, empty.Body.String())
	require.Contains(t, empty.Body.String(), `"days":[]`)
	require.True(t, decode[discipleReportsResponse](t, empty).IsDiscipleMaker)
}

func TestOnlyTheAuthorityChosenSeesTheReports(t *testing.T) {
	router := newTestRouter(t)
	me := registerMember(t, router, nil)
	zoe := registerMember(t, router, nil)
	other := registerMember(t, router, nil)
	asking := registerMember(t, router, nil)
	refused := registerMember(t, router, nil)
	accepted := chooseDiscipleMaker(t, router, zoe, me)
	require.Equal(t, http.StatusOK, saveDay(t, router, zoe.Token, daysAgo(0), map[string]any{}).Code)
	require.Equal(t, http.StatusOK, discipleReports(t, router, me.Token, accepted.ID, "").Code)

	notFound := func(token string, id int64) {
		t.Helper()
		response := discipleReports(t, router, token, id, "")
		require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		require.Equal(t, "NOT_FOUND", errorCode(t, response))
	}
	// the link isn't theirs to read: Zoe herself, another member
	notFound(zoe.Token, accepted.ID)
	notFound(other.Token, accepted.ID)

	// not answered yet, or declined
	pending := decode[authorityResponse](t, send(t, router, http.MethodPost, asking.Token, "/v1/authorities/",
		map[string]any{"username": me.Username, "is_disciple_maker": false}))
	notFound(me.Token, pending.ID)
	declined := decode[authorityResponse](t, send(t, router, http.MethodPost, refused.Token, "/v1/authorities/",
		map[string]any{"username": me.Username, "is_disciple_maker": true}))
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPost, me.Token,
		fmt.Sprintf("/v1/authorities/requests/%d/decline", declined.ID), nil).Code)
	notFound(me.Token, declined.ID)

	// typed by hand: nobody's account
	byHand := send(t, router, http.MethodPost, zoe.Token, "/v1/authorities/", map[string]any{
		"first_name": "Marc", "phone_number": "+237690000001", "is_disciple_maker": false,
	})
	require.Equal(t, http.StatusCreated, byHand.Code, byHand.Body.String())
	notFound(me.Token, decode[authorityResponse](t, byHand).ID)

	notFound(me.Token, accepted.ID+1_000_000)
	require.Equal(t, http.StatusBadRequest, get(t, router, me.Token, "/v1/disciples/abc/reports", nil).Code)
	require.Equal(t, http.StatusUnauthorized, discipleReports(t, router, "", accepted.ID, "").Code)
}

func TestTheRangeOfTheReports(t *testing.T) {
	router := newTestRouter(t)
	me := registerMember(t, router, nil)
	zoe := registerMember(t, router, nil)
	authority := chooseAuthority(t, router, zoe, me, false)
	for _, n := range []int{0, 29, 30, 91, 92} {
		require.Equal(t, http.StatusOK, saveDay(t, router, zoe.Token, daysAgo(n), map[string]any{}).Code)
	}
	dates := func(query string) []string {
		t.Helper()
		response := discipleReports(t, router, me.Token, authority.ID, query)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		dates := []string{}
		for _, day := range decode[discipleReportsResponse](t, response).Days {
			dates = append(dates, day.Date)
		}
		return dates
	}

	// by default the 30 days ending today; a to in the future stops at today
	require.Equal(t, []string{daysAgo(0), daysAgo(29)}, dates(""))
	require.Equal(t, []string{daysAgo(0), daysAgo(29)}, dates("?to="+daysAgo(-3)))
	// the 30 days ending a given to
	require.Equal(t, []string{daysAgo(29), daysAgo(30)}, dates("?to="+daysAgo(1)))
	// 92 days at most
	require.Equal(t, []string{daysAgo(0), daysAgo(29), daysAgo(30), daysAgo(91)}, dates("?from="+daysAgo(91)))
	require.Equal(t, []string{daysAgo(29), daysAgo(30), daysAgo(91), daysAgo(92)},
		dates("?from="+daysAgo(92)+"&to="+daysAgo(1)))

	for _, query := range []string{
		"?from=" + daysAgo(92),
		"?from=" + daysAgo(200) + "&to=" + daysAgo(10),
		"?from=" + daysAgo(1) + "&to=" + daysAgo(2),
		"?from=" + daysAgo(-1),
		"?from=" + daysAgo(-1) + "&to=" + daysAgo(-5),
		"?from=14-10-2026",
		"?to=2026-13-01",
	} {
		response := discipleReports(t, router, me.Token, authority.ID, query)
		require.Equal(t, http.StatusBadRequest, response.Code, "%s: %s", query, response.Body.String())
	}
}

func TestTheDisciplesComeAPageAtATime(t *testing.T) {
	router := newTestRouter(t)
	me := registerMember(t, router, nil)

	// 5 disciples whose names come last by name, and 20 other members
	makers, others := []string{}, []string{}
	var last member
	for i := range 25 {
		discipleMaker := i%5 == 0
		name := fmt.Sprintf("A%02d", i)
		if discipleMaker {
			name = fmt.Sprintf("Z%02d", i)
		}
		someone := registerMember(t, router, map[string]string{"first_name": name + uniqueName()})
		chooseAuthority(t, router, someone, me, discipleMaker)
		if discipleMaker {
			makers = append(makers, someone.Username)
		} else {
			others = append(others, someone.Username)
		}
		last = someone
	}
	// the last one, on the second page, filled in today
	require.Equal(t, http.StatusOK, saveDay(t, router, last.Token, daysAgo(0), map[string]any{}).Code)
	want := append(makers, others...)

	first := listDisciplesPage(t, router, me.Token, "")
	require.Len(t, first.Content, 20)
	require.Equal(t, MetaResponse{Total: 25, Limit: 20, Offset: 0}, first.Meta)
	second := listDisciplesPage(t, router, me.Token, "?page=2&today="+daysAgo(0))
	require.Len(t, second.Content, 5)
	require.Equal(t, MetaResponse{Total: 25, Limit: 20, Offset: 20}, second.Meta)
	// the disciples first, then by name, across the pages
	require.Equal(t, want, append(usernamesOf(first.Content), usernamesOf(second.Content)...))
	require.True(t, first.Content[4].IsDiscipleMaker)
	require.False(t, first.Content[5].IsDiscipleMaker)
	// the regularity of the members of the page
	require.Equal(t, last.Username, second.Content[4].Member.Username)
	require.Equal(t, []string{daysAgo(0)}, second.Content[4].Regularity.Days)
	require.Empty(t, second.Content[3].Regularity.Days)

	// another size
	page := listDisciplesPage(t, router, me.Token, "?page=3&size=10")
	require.Equal(t, MetaResponse{Total: 25, Limit: 10, Offset: 20}, page.Meta)
	require.Equal(t, want[20:], usernamesOf(page.Content))
	// past the end
	page = listDisciplesPage(t, router, me.Token, "?page=3")
	require.Empty(t, page.Content)
	require.Equal(t, int64(25), page.Meta.Total)
	require.Equal(t, http.StatusBadRequest, get(t, router, me.Token, "/v1/disciples?page=abc", nil).Code)
}

func TestTheDisciplesBySearch(t *testing.T) {
	router := newTestRouter(t)
	me := registerMember(t, router, nil)
	family := uniqueName()
	accented := registerMember(t, router, map[string]string{"first_name": "Élodie", "last_name": family})
	plain := registerMember(t, router, map[string]string{"first_name": "Elodie", "last_name": uniqueName()})
	pseudo := "frere." + strings.ToLower(uniqueName())
	marc := registerMember(t, router, map[string]string{"first_name": "Marc", "username": pseudo})
	chooseAuthority(t, router, accented, me, false)
	chooseDiscipleMaker(t, router, plain, me)
	chooseAuthority(t, router, marc, me, false)

	found := func(search string) []string {
		t.Helper()
		page := listDisciplesPage(t, router, me.Token, "?search="+url.QueryEscape(search))
		require.Equal(t, int64(len(page.Content)), page.Meta.Total, search)
		return usernamesOf(page.Content)
	}
	both := []string{plain.Username, accented.Username}

	// by first name, case and accents left aside, in the list's order
	require.Equal(t, both, found("élodie"))
	require.Equal(t, both, found("Elodie"))
	require.Equal(t, both, found("ÉLODIE"))
	require.Equal(t, both, found("lodi"))
	// by last name, and the whole name either way
	require.Equal(t, []string{accented.Username}, found(strings.ToUpper(family)))
	require.Equal(t, []string{accented.Username}, found("elodie "+family))
	require.Equal(t, []string{accented.Username}, found(family+" Élodie"))
	require.Equal(t, []string{plain.Username}, found(plain.LastName+" elodie"))
	// by pseudo, with or without the @
	require.Equal(t, []string{marc.Username}, found("@"+pseudo))
	require.Equal(t, []string{marc.Username}, found("@"+strings.ToUpper(pseudo)))
	require.Equal(t, []string{marc.Username}, found(pseudo[len("frere."):]))
	// blank: everyone
	require.Len(t, found("  "), 3)
	require.Len(t, found(""), 3)

	// nothing found
	response := get(t, router, me.Token, "/v1/disciples?search=personne", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"content":[]`)
	require.Contains(t, response.Body.String(), `"meta":{"total":0,"limit":20,"offset":0}`)

	// the pages of the members found
	page := listDisciplesPage(t, router, me.Token, "?search=elodie&size=1&page=2")
	require.Equal(t, MetaResponse{Total: 2, Limit: 1, Offset: 1}, page.Meta)
	require.Equal(t, []string{accented.Username}, usernamesOf(page.Content))
	require.False(t, page.Content[0].IsDiscipleMaker)
	require.NotNil(t, page.Content[0].Regularity)
}
