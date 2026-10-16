package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// listItems: GET /v1/activities, by code.
func listItems(t *testing.T, router http.Handler, token string) map[string]activityTypeResponse {
	t.Helper()
	response := get(t, router, token, "/v1/activities/", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	items := map[string]activityTypeResponse{}
	for _, item := range decode[[]activityTypeResponse](t, response) {
		items[item.Code] = item
	}
	return items
}

func setGoal(t *testing.T, router http.Handler, token string, id int64, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return send(t, router, http.MethodPut, token, fmt.Sprintf("/v1/activities/%d/goal", id), body)
}

func TestActivityGoalIsKeptOnTheAccount(t *testing.T) {
	router := newTestRouter(t)
	grace := registerMember(t, router, nil)
	items := listItems(t, router, grace.Token)
	require.Nil(t, items["LB"].Goal, "no goal at first")

	set := setGoal(t, router, grace.Token, items["LB"].ID, map[string]any{"quantity": 3})
	require.Equal(t, http.StatusOK, set.Code, set.Body.String())
	require.Equal(t, int32(3), *decode[activityGoalResponse](t, set).Quantity)

	set = setGoal(t, router, grace.Token, items["PS"].ID, map[string]any{"minutes": 60})
	require.Equal(t, http.StatusOK, set.Code, set.Body.String())

	// read again, as another phone would
	items = listItems(t, router, grace.Token)
	require.Equal(t, int32(3), *items["LB"].Goal.Quantity)
	require.Nil(t, items["LB"].Goal.Minutes)
	require.Equal(t, int32(60), *items["PS"].Goal.Minutes)

	// changed, then removed
	set = setGoal(t, router, grace.Token, items["LB"].ID, map[string]any{"quantity": 5})
	require.Equal(t, http.StatusOK, set.Code, set.Body.String())
	require.Equal(t, int32(5), *listItems(t, router, grace.Token)["LB"].Goal.Quantity)

	removed := setGoal(t, router, grace.Token, items["LB"].ID, map[string]any{})
	require.Equal(t, http.StatusNoContent, removed.Code, removed.Body.String())
	items = listItems(t, router, grace.Token)
	require.Nil(t, items["LB"].Goal)
	require.NotNil(t, items["PS"].Goal, "the other goals stay")
}

func TestActivityGoalOnAPersonalItem(t *testing.T) {
	router := newTestRouter(t)
	grace := registerMember(t, router, nil)
	created := send(t, router, http.MethodPost, grace.Token, "/v1/activities/", map[string]any{
		"code": "RQDQA", "label": "RQDQA", "tracks_quantity": true, "tracks_duration": true,
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	item := decode[activityTypeResponse](t, created)

	set := setGoal(t, router, grace.Token, item.ID, map[string]any{"quantity": 2, "minutes": 30})
	require.Equal(t, http.StatusOK, set.Code, set.Body.String())

	goal := listItems(t, router, grace.Token)["RQDQA"].Goal
	require.Equal(t, int32(2), *goal.Quantity)
	require.Equal(t, int32(30), *goal.Minutes)

	// an archived item is no longer listed: its goal can't be set
	archived := send(t, router, http.MethodDelete, grace.Token, fmt.Sprintf("/v1/activities/%d", item.ID), nil)
	require.Equal(t, http.StatusNoContent, archived.Code, archived.Body.String())
	set = setGoal(t, router, grace.Token, item.ID, map[string]any{"quantity": 3})
	require.Equal(t, http.StatusNotFound, set.Code, set.Body.String())
}

func TestActivityGoalFitsTheItem(t *testing.T) {
	router := newTestRouter(t)
	grace := registerMember(t, router, nil)
	items := listItems(t, router, grace.Token)

	// LB counts chapters, PS a duration
	wrong := setGoal(t, router, grace.Token, items["LB"].ID, map[string]any{"minutes": 30})
	require.Equal(t, http.StatusBadRequest, wrong.Code, wrong.Body.String())
	wrong = setGoal(t, router, grace.Token, items["PS"].ID, map[string]any{"quantity": 2})
	require.Equal(t, http.StatusBadRequest, wrong.Code, wrong.Body.String())

	for _, body := range []map[string]any{
		{"quantity": 0},
		{"quantity": -1},
		{"quantity": 1_000_001},
	} {
		response := setGoal(t, router, grace.Token, items["LB"].ID, body)
		require.Equal(t, http.StatusBadRequest, response.Code, "%v: %s", body, response.Body.String())
	}
	tooLong := setGoal(t, router, grace.Token, items["PS"].ID, map[string]any{"minutes": 1441})
	require.Equal(t, http.StatusBadRequest, tooLong.Code, tooLong.Body.String())

	unknown := setGoal(t, router, grace.Token, 999_999_999, map[string]any{"quantity": 1})
	require.Equal(t, http.StatusNotFound, unknown.Code, unknown.Body.String())

	anonymous := setGoal(t, router, "", items["LB"].ID, map[string]any{"quantity": 1})
	require.Equal(t, http.StatusUnauthorized, anonymous.Code, anonymous.Body.String())
}

func TestActivityGoalsBelongToEachMember(t *testing.T) {
	router := newTestRouter(t)
	grace := registerMember(t, router, nil)
	paul := registerMember(t, router, nil)
	lb := listItems(t, router, grace.Token)["LB"].ID

	require.Equal(t, http.StatusOK, setGoal(t, router, grace.Token, lb, map[string]any{"quantity": 4}).Code)
	require.Equal(t, http.StatusOK, setGoal(t, router, paul.Token, lb, map[string]any{"quantity": 1}).Code)

	require.Equal(t, int32(4), *listItems(t, router, grace.Token)["LB"].Goal.Quantity)
	require.Equal(t, int32(1), *listItems(t, router, paul.Token)["LB"].Goal.Quantity)

	// someone else's personal item
	created := send(t, router, http.MethodPost, paul.Token, "/v1/activities/", map[string]any{
		"code": "MINE", "label": "Mine", "tracks_quantity": true,
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	mine := decode[activityTypeResponse](t, created)
	other := setGoal(t, router, grace.Token, mine.ID, map[string]any{"quantity": 1})
	require.Equal(t, http.StatusNotFound, other.Code, other.Body.String())
}
