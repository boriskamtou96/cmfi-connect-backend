package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// setGameClock sets the time of the games for the test.
func setGameClock(t *testing.T, now time.Time) {
	previous := gameClock
	gameClock = func() time.Time { return now }
	t.Cleanup(func() { gameClock = previous })
}

func november(day, hour, minute int) time.Time {
	return time.Date(2026, time.November, day, hour, minute, 0, 0, time.UTC)
}

// lessonAnswers answers the questions of an open lesson in order: 'J' right
// at once, 'F' wrong first then right on the second pass.
func lessonAnswers(t *testing.T, router http.Handler, token string, lessonID int64, marks string) []map[string]any {
	response := send(t, router, http.MethodGet, token, fmt.Sprintf("/v1/games/lessons/%d", lessonID), nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	lesson := decode[gameLessonResponse](t, response)
	require.GreaterOrEqual(t, len(lesson.Questions), len(marks))

	var first, second []map[string]any
	for i, mark := range marks {
		question := lesson.Questions[i]
		if mark == 'J' {
			first = append(first, map[string]any{"question_id": question.ID, "choice": question.Answer, "attempt": 1})
			continue
		}
		first = append(first, map[string]any{"question_id": question.ID, "choice": "b", "attempt": 1})
		second = append(second, map[string]any{"question_id": question.ID, "choice": question.Answer, "attempt": 2})
	}
	return append(first, second...)
}

func sendLesson(t *testing.T, router http.Handler, token string, lessonID int64, clientID string, answers []map[string]any) *httptest.ResponseRecorder {
	return send(t, router, http.MethodPost, token, fmt.Sprintf("/v1/games/lessons/%d/results", lessonID),
		map[string]any{"client_id": clientID, "answers": answers})
}

// playLesson plays a lesson and gives its result.
func playLesson(t *testing.T, router http.Handler, token string, lessonID int64, clientID, marks string) gameResultResponse {
	response := sendLesson(t, router, token, lessonID, clientID, lessonAnswers(t, router, token, lessonID, marks))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[gameResultResponse](t, response)
}

func TestALessonPlayedGivesPointsStarsAndOpensTheNext(t *testing.T) {
	router := newTestRouter(t)
	code, lessons := importSampleCourse(t, router, 2)
	m := registerMember(t, router, nil)
	setGameClock(t, november(12, 10, 0))

	result := playLesson(t, router, m.Token, lessons[0], "play-1", "JJJJJJJJ")
	require.Equal(t, 105, result.Points)
	require.Equal(t, 8, result.FirstTry)
	require.Equal(t, 8, result.Total)
	require.Equal(t, 3, result.Stars)
	require.Equal(t, 3, result.BestStars)
	require.True(t, result.Perfect)
	require.Equal(t, 5, result.ComboBonus)
	require.False(t, result.Replay)
	require.False(t, result.LevelUp)
	require.False(t, result.LevelCompleted)
	require.NotNil(t, result.NextLessonID)
	require.Equal(t, lessons[1], *result.NextLessonID)
	require.Equal(t, 105, result.Player.TotalPoints)
	require.Equal(t, 1, result.Player.StreakDays)
	require.True(t, result.Player.PlayedToday)
	require.Equal(t, 105, result.Player.PointsToday)

	require.Equal(t, []string{"done", "open"}, pathStates(getPath(t, router, m.Token, code)))

	// the last lesson of the level, with two mistakes caught
	last := playLesson(t, router, m.Token, lessons[1], "play-2", "JJFJJJJF")
	require.Equal(t, 70, last.Points)
	require.Equal(t, 2, last.Stars)
	require.False(t, last.Perfect)
	require.True(t, last.LevelCompleted)
	require.Nil(t, last.NextLessonID)
	require.Equal(t, 175, last.Player.TotalPoints)
}

func TestTheSameResultSentTwiceCountsOnce(t *testing.T) {
	router := newTestRouter(t)
	_, lessons := importSampleCourse(t, router, 2)
	m := registerMember(t, router, nil)
	setGameClock(t, november(12, 10, 0))

	answers := lessonAnswers(t, router, m.Token, lessons[0], "JJJJJJJJ")
	first := sendLesson(t, router, m.Token, lessons[0], "same-play", answers)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	again := sendLesson(t, router, m.Token, lessons[0], "same-play", answers)
	require.Equal(t, http.StatusOK, again.Code, again.Body.String())

	require.Equal(t, decode[gameResultResponse](t, first), decode[gameResultResponse](t, again))
	require.Equal(t, 105, decode[gameResultResponse](t, again).Player.TotalPoints)
	var plays int
	require.NoError(t, testConn(t).QueryRow(`SELECT COUNT(*) FROM game_plays WHERE user_id = $1`, m.ID).Scan(&plays))
	require.Equal(t, 1, plays)
}

func TestALessonReplayedGivesFewPointsCappedByDay(t *testing.T) {
	router := newTestRouter(t)
	_, lessons := importSampleCourse(t, router, 1)
	m := registerMember(t, router, nil)
	setGameClock(t, november(12, 10, 0))
	playLesson(t, router, m.Token, lessons[0], "first", "JJJJJJJJ")

	for i, points := range []int{16, 4, 0} {
		replay := playLesson(t, router, m.Token, lessons[0], fmt.Sprintf("again-%d", i), "JJJJJJJJ")
		require.True(t, replay.Replay)
		require.Equal(t, points, replay.Points, "replay %d", i+1)
		require.Zero(t, replay.ComboBonus)
	}
	// the next day the replays count again
	setGameClock(t, november(13, 10, 0))
	replay := playLesson(t, router, m.Token, lessons[0], "next-day", "JJJJJJJJ")
	require.Equal(t, 16, replay.Points)
	require.Equal(t, 105+20+16, replay.Player.TotalPoints)
}

func TestAReplayKeepsTheBestStars(t *testing.T) {
	router := newTestRouter(t)
	code, lessons := importSampleCourse(t, router, 1)
	m := registerMember(t, router, nil)
	setGameClock(t, november(12, 10, 0))

	require.Equal(t, 2, playLesson(t, router, m.Token, lessons[0], "p1", "JJFJJJJF").BestStars)
	better := playLesson(t, router, m.Token, lessons[0], "p2", "JJJJJJJJ")
	require.Equal(t, 3, better.Stars)
	require.Equal(t, 3, better.BestStars)
	worse := playLesson(t, router, m.Token, lessons[0], "p3", "FFFFJJJJ")
	require.Equal(t, 1, worse.Stars)
	require.Equal(t, 3, worse.BestStars)
	require.Equal(t, 3, getPath(t, router, m.Token, code).Levels[0].Lessons[0].Stars)
}

func TestAnswersToQuestionsGoneAreIgnored(t *testing.T) {
	router := newTestRouter(t)
	_, lessons := importSampleCourse(t, router, 2)
	m := registerMember(t, router, nil)
	setGameClock(t, november(12, 10, 0))

	// two questions are retired after the phone downloaded the lesson
	answers := lessonAnswers(t, router, m.Token, lessons[0], "JJJJJJFF")
	_, err := testConn(t).Exec(`UPDATE game_questions SET is_active = FALSE WHERE id IN ($1, $2)`,
		answers[6]["question_id"], answers[7]["question_id"])
	require.NoError(t, err)
	response := sendLesson(t, router, m.Token, lessons[0], "gone", answers)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	result := decode[gameResultResponse](t, response)
	require.Equal(t, 6, result.Total)
	require.Equal(t, 85, result.Points)
	require.True(t, result.Perfect)

	// nothing in common with the lesson, or nothing at all: refused, nothing kept
	for name, body := range map[string]map[string]any{
		"unknown questions": {"client_id": "x1", "answers": []map[string]any{{"question_id": 999999999, "choice": "a", "attempt": 1}}},
		"no answer":         {"client_id": "x2", "answers": []map[string]any{}},
		"no client id":      {"client_id": " ", "answers": answers},
		"a long client id":  {"client_id": strings.Repeat("x", 65), "answers": answers},
	} {
		refused := send(t, router, http.MethodPost, m.Token, fmt.Sprintf("/v1/games/lessons/%d/results", lessons[1]), body)
		require.Equal(t, http.StatusBadRequest, refused.Code, "%s: %s", name, refused.Body.String())
	}
	require.Equal(t, 85, getPath(t, router, m.Token, "").Player.TotalPoints)

	// a lesson not open yet, a lesson that does not exist
	other := registerMember(t, router, nil)
	locked := sendLesson(t, router, other.Token, lessons[1], "y1", answers)
	require.Equal(t, http.StatusConflict, locked.Code, locked.Body.String())
	require.Equal(t, "LESSON_LOCKED", errorCode(t, locked))
	require.Equal(t, http.StatusNotFound, sendLesson(t, router, other.Token, 999999999, "y2", answers).Code)
}

func TestAPlayLateInTheEveningCountsForTheNextDayInDouala(t *testing.T) {
	router := newTestRouter(t)
	code, lessons := importSampleCourse(t, router, 1)
	m := registerMember(t, router, nil)

	// 23:30 UTC on the 12th is 00:30 on the 13th in Douala
	setGameClock(t, november(12, 23, 30))
	playLesson(t, router, m.Token, lessons[0], "late", "JJJJJJJJ")

	setGameClock(t, november(13, 10, 0))
	player := getPath(t, router, m.Token, code).Player
	require.True(t, player.PlayedToday)
	require.Equal(t, 105, player.PointsToday)
	require.Equal(t, 1, player.StreakDays)

	// 23:10 UTC on the 13th: the 14th has begun in Douala
	setGameClock(t, november(13, 23, 10))
	player = getPath(t, router, m.Token, code).Player
	require.False(t, player.PlayedToday)
	require.Zero(t, player.PointsToday)
	require.Equal(t, 1, player.StreakDays)
}

func TestTheStreakGrowsDayAfterDayAndForgivesOneMissedDay(t *testing.T) {
	router := newTestRouter(t)
	code, lessons := importSampleCourse(t, router, 4)
	m := registerMember(t, router, nil)

	// Monday 9, Tuesday 10, nothing on Wednesday 11, Thursday 12
	for i, day := range []int{9, 10, 12} {
		setGameClock(t, november(day, 10, 0))
		result := playLesson(t, router, m.Token, lessons[i], fmt.Sprintf("day-%d", day), "JJJJJJJJ")
		require.Equal(t, i+1, result.Player.StreakDays, "day %d", day)
	}
	// two days without playing: the streak is broken, and starts again
	setGameClock(t, november(15, 10, 0))
	require.Zero(t, getPath(t, router, m.Token, code).Player.StreakDays)
	require.Equal(t, 1, playLesson(t, router, m.Token, lessons[3], "day-15", "JJJJJJJJ").Player.StreakDays)
}

func TestPassingAThresholdIsALevelUp(t *testing.T) {
	router := newTestRouter(t)
	_, lessons := importSampleCourse(t, router, 2)
	m := registerMember(t, router, nil)
	setGameClock(t, november(12, 10, 0))

	first := playLesson(t, router, m.Token, lessons[0], "l1", "JJJJJJJJ")
	require.False(t, first.LevelUp)
	require.Equal(t, 1, first.Player.Level)

	// 210 points: level 2 begins at 200
	answers := lessonAnswers(t, router, m.Token, lessons[1], "JJJJJJJJ")
	second := decode[gameResultResponse](t, sendLesson(t, router, m.Token, lessons[1], "l2", answers))
	require.True(t, second.LevelUp)
	require.Equal(t, 2, second.Player.Level)
	require.Equal(t, 200, second.Player.LevelFloor)
	require.Equal(t, 600, second.Player.NextLevelAt)
	// sent again: the same news
	require.True(t, decode[gameResultResponse](t, sendLesson(t, router, m.Token, lessons[1], "l2", answers)).LevelUp)
}
