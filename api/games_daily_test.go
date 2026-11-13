package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func getDaily(t *testing.T, router http.Handler, m member) gameDailyResponse {
	response := send(t, router, http.MethodGet, m.Token, "/v1/games/daily", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[gameDailyResponse](t, response)
}

func questionIDs(questions []gameQuestionResponse) []int64 {
	ids := []int64{}
	for _, question := range questions {
		ids = append(ids, question.ID)
	}
	return ids
}

// sendDaily answers the challenge of the day, all right.
func sendDaily(t *testing.T, router http.Handler, m member, clientID string) *httptest.ResponseRecorder {
	var answers []map[string]any
	for _, question := range getDaily(t, router, m).Questions {
		answers = append(answers, map[string]any{"question_id": question.ID, "choice": question.Answer, "attempt": 1})
	}
	return send(t, router, http.MethodPost, m.Token, "/v1/games/daily/results", map[string]any{"client_id": clientID, "answers": answers})
}

func TestTheDailyChallengeNeedsALessonDone(t *testing.T) {
	router := newTestRouter(t)
	importSampleCourse(t, router, 1)
	m := registerMember(t, router, nil)

	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodGet, m.Token, "/v1/games/daily", nil).Code)
	refused := send(t, router, http.MethodPost, m.Token, "/v1/games/daily/results",
		map[string]any{"client_id": "d", "answers": []map[string]any{{"question_id": 1, "choice": "a", "attempt": 1}}})
	require.Equal(t, http.StatusNotFound, refused.Code, refused.Body.String())
}

func TestTheDailyChallengeIsTheSameAllDayAndChangesTheNextDay(t *testing.T) {
	router := newTestRouter(t)
	_, lessons := importSampleCourse(t, router, 3)
	m := registerMember(t, router, nil)
	setGameClock(t, november(12, 8, 0))
	first := lessonAnswers(t, router, m.Token, lessons[0], "JJJJJJJJ")
	require.Equal(t, http.StatusOK, sendLesson(t, router, m.Token, lessons[0], "l0", first).Code)
	playLesson(t, router, m.Token, lessons[1], "l1", "JJJJJJJJ")

	morning := getDaily(t, router, m)
	require.False(t, morning.Done)
	require.Len(t, morning.Questions, 5)
	require.Equal(t, "a", morning.Questions[0].Answer)
	// drawn from the lessons done only: the third one is not
	third := send(t, router, http.MethodGet, m.Token, "/v1/games/lessons/"+itoa(lessons[2]), nil)
	for _, id := range questionIDs(morning.Questions) {
		require.NotContains(t, questionIDs(decode[gameLessonResponse](t, third).Questions), id)
	}

	setGameClock(t, november(12, 20, 0))
	require.Equal(t, questionIDs(morning.Questions), questionIDs(getDaily(t, router, m).Questions))

	// another draw on the days that follow
	changed := false
	for day := 13; day <= 17; day++ {
		setGameClock(t, november(day, 8, 0))
		changed = changed || !equalIDs(questionIDs(morning.Questions), questionIDs(getDaily(t, router, m).Questions))
	}
	require.True(t, changed)
}

func TestTheDailyChallengeGivesFifteenPointsOnceADay(t *testing.T) {
	router := newTestRouter(t)
	_, lessons := importSampleCourse(t, router, 1)
	m := registerMember(t, router, nil)
	setGameClock(t, november(12, 8, 0))
	playLesson(t, router, m.Token, lessons[0], "l0", "JJJJJJJJ")

	done := sendDaily(t, router, m, "daily-12")
	require.Equal(t, http.StatusOK, done.Code, done.Body.String())
	result := decode[gameDailyResultResponse](t, done)
	require.Equal(t, 15, result.Points)
	require.Equal(t, 120, result.Player.TotalPoints)
	require.Equal(t, 120, result.Player.PointsToday)
	require.True(t, result.Player.DailyDone)
	require.True(t, getDaily(t, router, m).Done)

	// sent again: the same answer; done again the same day: nothing more
	require.Equal(t, result, decode[gameDailyResultResponse](t, sendDaily(t, router, m, "daily-12")))
	again := decode[gameDailyResultResponse](t, sendDaily(t, router, m, "daily-12-bis"))
	require.Zero(t, again.Points)
	require.Equal(t, 120, again.Player.TotalPoints)

	// the next day: a new challenge, which keeps the streak going
	setGameClock(t, november(13, 8, 0))
	require.False(t, getDaily(t, router, m).Done)
	next := decode[gameDailyResultResponse](t, sendDaily(t, router, m, "daily-13"))
	require.Equal(t, 15, next.Points)
	require.Equal(t, 135, next.Player.TotalPoints)
	require.Equal(t, 2, next.Player.StreakDays)

	// answers about other questions: refused
	setGameClock(t, november(14, 8, 0))
	refused := send(t, router, http.MethodPost, m.Token, "/v1/games/daily/results",
		map[string]any{"client_id": "d14", "answers": []map[string]any{{"question_id": 999999999, "choice": "a", "attempt": 1}}})
	require.Equal(t, http.StatusBadRequest, refused.Code, refused.Body.String())
	require.False(t, getDaily(t, router, m).Done)
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAChallengeOpenedBeforeMidnightIsStillAcceptedAfter(t *testing.T) {
	router := newTestRouter(t)
	_, lessons := importSampleCourse(t, router, 3)
	m := registerMember(t, router, nil)
	setGameClock(t, november(12, 8, 0))
	for i, lesson := range lessons {
		playLesson(t, router, m.Token, lesson, "l"+itoa(int64(i)), "JJJJJJJJ")
	}

	// opened at 23:59 in Douala, sent at 00:01: yesterday's questions
	setGameClock(t, november(12, 22, 59))
	var answers []map[string]any
	for _, question := range getDaily(t, router, m).Questions {
		answers = append(answers, map[string]any{"question_id": question.ID, "choice": question.Answer, "attempt": 1})
	}
	setGameClock(t, november(12, 23, 1))
	sent := send(t, router, http.MethodPost, m.Token, "/v1/games/daily/results", map[string]any{"client_id": "midnight", "answers": answers})
	require.Equal(t, http.StatusOK, sent.Code, sent.Body.String())
	result := decode[gameDailyResultResponse](t, sent)
	require.Equal(t, 15, result.Points)
	require.Equal(t, 5, result.Total)
}
