package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTheBibleReadingIsCountedInChaptersAndInTime(t *testing.T) {
	router := newTestRouter(t)
	grace := registerMember(t, router, nil)

	reading := listItems(t, router, grace.Token)["LB"]
	require.True(t, reading.TracksQuantity)
	require.True(t, reading.TracksDuration)
	// a goal in minutes too
	require.Equal(t, http.StatusOK, setGoal(t, router, grace.Token, reading.ID, map[string]any{"minutes": 20}).Code)

	// 3 chapters in 25 minutes
	saved := saveDay(t, router, grace.Token, daysAgo(0), map[string]any{
		"entries": []map[string]any{{"activity_type_id": reading.ID, "quantity": 3, "duration_minutes": 25}},
	})
	require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
	require.Contains(t, saved.Body.String(), `"duration_minutes":25`)
}
