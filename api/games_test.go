package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func pathStates(path gamePathResponse) []string {
	var states []string
	for _, level := range path.Levels {
		for _, lesson := range level.Lessons {
			states = append(states, lesson.State)
		}
	}
	return states
}

func getPath(t *testing.T, router http.Handler, token, code string) gamePathResponse {
	response := send(t, router, http.MethodGet, token, "/v1/games/path?course="+code, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[gamePathResponse](t, response)
}

func TestThePathOpensOnItsFirstLesson(t *testing.T) {
	router := newTestRouter(t)
	code, lessons := importSampleCourse(t, router, 3)
	m := registerMember(t, router, nil)

	path := getPath(t, router, m.Token, code)
	require.Equal(t, code, path.Course)
	require.Contains(t, path.Courses, gameCourseResponse{Code: code, Title: "Notre ministère"})
	require.Len(t, path.Levels, 1)
	level := path.Levels[0]
	require.Equal(t, "Notre fondateur", level.Title)
	require.Equal(t, "Sa vie", level.Description)
	require.Equal(t, "#0B3B70", level.Color)
	require.Len(t, level.Lessons, 3)
	require.Equal(t, lessons[0], level.Lessons[0].ID)
	require.Equal(t, "Leçon 1", level.Lessons[0].Title)
	require.Equal(t, []string{"open", "locked", "locked"}, pathStates(path))

	// a member who never played: everything at zero, no rank
	require.Equal(t, gamePlayerResponse{Level: 1, NextLevelAt: 200, DailyGoal: 50}, path.Player)

	// the first lesson done: its stars, and the second one opens
	markLessonDone(t, m.ID, lessons[0])
	path = getPath(t, router, m.Token, code)
	require.Equal(t, []string{"done", "open", "locked"}, pathStates(path))
	require.Equal(t, 3, path.Levels[0].Lessons[0].Stars)
}

func TestALessonComesInTheLanguageAskedOrInFrench(t *testing.T) {
	router := newTestRouter(t)
	code, lessons := importSampleCourse(t, router, 1)
	m := registerMember(t, router, nil)

	english := decode[gamePathResponse](t, send(t, router, http.MethodGet, m.Token, "/v1/games/path?lang=en&course="+code, nil))
	require.Contains(t, english.Courses, gameCourseResponse{Code: code, Title: "Our ministry"})
	// no English title for the level: the French one
	require.Equal(t, "Notre fondateur", english.Levels[0].Title)

	response := send(t, router, http.MethodGet, m.Token, fmt.Sprintf("/v1/games/lessons/%d?lang=en", lessons[0]), nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	lesson := decode[gameLessonResponse](t, response)
	require.Equal(t, lessons[0], lesson.ID)
	require.Equal(t, "Leçon 1", lesson.Title)
	require.Equal(t, "Notre fondateur", lesson.LevelTitle)
	require.Equal(t, code, lesson.Course)
	require.Len(t, lesson.Questions, 8)
	question := lesson.Questions[0]
	require.Equal(t, "choice", question.Kind)
	require.Equal(t, "Question 1 de la leçon 1 ?", question.Prompt)
	require.Equal(t, []gameChoiceResponse{{ID: "a", Text: "The right one"}, {ID: "b", Text: "Une autre"}, {ID: "c", Text: "Encore une autre"}}, question.Choices)
	require.Equal(t, "a", question.Answer)
	require.Equal(t, "Parce que.", question.Explanation)
	require.Equal(t, "ministry:founder", question.Source)
}

func TestALockedLessonIsRefused(t *testing.T) {
	router := newTestRouter(t)
	_, lessons := importSampleCourse(t, router, 2)
	m := registerMember(t, router, nil)

	locked := send(t, router, http.MethodGet, m.Token, fmt.Sprintf("/v1/games/lessons/%d", lessons[1]), nil)
	require.Equal(t, http.StatusConflict, locked.Code, locked.Body.String())
	require.Equal(t, "LESSON_LOCKED", errorCode(t, locked))

	markLessonDone(t, m.ID, lessons[0])
	open := send(t, router, http.MethodGet, m.Token, fmt.Sprintf("/v1/games/lessons/%d", lessons[1]), nil)
	require.Equal(t, http.StatusOK, open.Code, open.Body.String())
}

func TestAnUnpublishedCourseIsNotFound(t *testing.T) {
	router := newTestRouter(t)
	m := registerMember(t, router, nil)
	code := newCourseCode()
	t.Cleanup(func() { _ = testStore.DeleteGameCourse(context.Background(), code) })
	draft := sampleCourse(code, 1)
	draft["published"] = false
	imported := send(t, router, http.MethodPost, adminToken(t, router), "/v1/admin/games/import", draft)
	require.Equal(t, http.StatusOK, imported.Code, imported.Body.String())
	var lessonID int64
	require.NoError(t, testConn(t).QueryRow(
		`SELECT ls.id FROM game_lessons ls JOIN game_levels lv ON lv.id = ls.level_id JOIN game_courses c ON c.id = lv.course_id WHERE c.code = $1`, code,
	).Scan(&lessonID))

	for _, path := range []string{
		"/v1/games/path?course=" + code,
		"/v1/games/path?course=no-such-course",
		fmt.Sprintf("/v1/games/lessons/%d", lessonID),
		"/v1/games/lessons/999999999",
	} {
		response := send(t, router, http.MethodGet, m.Token, path, nil)
		require.Equal(t, http.StatusNotFound, response.Code, "%s: %s", path, response.Body.String())
	}
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodGet, m.Token, "/v1/games/lessons/abc", nil).Code)
	require.Equal(t, http.StatusUnauthorized, send(t, router, http.MethodGet, "", "/v1/games/path", nil).Code)
}

// testConn is a connection to the test database, for what no query reads.
func testConn(t *testing.T) *sql.DB {
	conn, err := sql.Open(dbDriver, testDSN())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
