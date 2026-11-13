package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func readGameStats(t *testing.T, router http.Handler, token string) gameStatsResponse {
	response := adminGames(t, router, http.MethodGet, token, "/stats", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[gameStatsResponse](t, response)
}

func TestTheFirstAnswersOfAFirstPlayAreCounted(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	_, lessons := importSampleCourse(t, router, 1)
	setGameClock(t, november(12, 10, 0))

	counts := func() [][2]int {
		var list [][2]int
		for _, question := range readLesson(t, router, token, lessons[0]).Questions {
			list = append(list, [2]int{question.Asked, question.Missed})
		}
		return list
	}
	require.Equal(t, [2]int{0, 0}, counts()[0], "nobody played yet")

	first := registerMember(t, router, nil)
	playLesson(t, router, first.Token, lessons[0], "play-1", "FJJJJJJJ")
	require.Equal(t, [2]int{1, 1}, counts()[0], "missed at the first answer, right at the second: one miss")
	require.Equal(t, [2]int{1, 0}, counts()[1])

	// the same play sent again, then the lesson played again: nothing more
	playLesson(t, router, first.Token, lessons[0], "play-1", "FJJJJJJJ")
	playLesson(t, router, first.Token, lessons[0], "play-2", "FFFFFFFF")
	require.Equal(t, [2]int{1, 1}, counts()[0])
	require.Equal(t, [2]int{1, 0}, counts()[1])

	second := registerMember(t, router, nil)
	playLesson(t, router, second.Token, lessons[0], "play-1", "JFJJJJJJ")
	require.Equal(t, [2]int{2, 1}, counts()[0])
	require.Equal(t, [2]int{2, 1}, counts()[1])
	require.Equal(t, [2]int{2, 0}, counts()[7])
}

func TestTheNumbersOfTheGames(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	code, lessons := importSampleCourse(t, router, 2)
	// Thursday; the week began on Monday the 9th
	setGameClock(t, november(12, 10, 0))

	member := registerMember(t, router, nil)
	require.Equal(t, http.StatusForbidden, adminGames(t, router, http.MethodGet, member.Token, "/stats", nil).Code)
	before := readGameStats(t, router, token)

	// five members miss the first question of the first lesson, one gets it
	for n := 0; n < 5; n++ {
		playLesson(t, router, registerMember(t, router, nil).Token, lessons[0], "play-1", "FJJJJJJJ")
	}
	playLesson(t, router, member.Token, lessons[0], "play-1", "JJJJJJJJ")
	playLesson(t, router, member.Token, lessons[1], "play-2", "JJJJJJJJ")
	choice := send(t, router, http.MethodPut, member.Token, "/v1/games/me", map[string]any{"in_leaderboard": true})
	require.Equal(t, http.StatusOK, choice.Code, choice.Body.String())

	after := readGameStats(t, router, token)
	require.Equal(t, before.Players.Total+6, after.Players.Total)
	require.Equal(t, before.Players.Week+6, after.Players.Week)
	require.Equal(t, before.Players.InLeaderboard+1, after.Players.InLeaderboard)
	require.Equal(t, before.LessonsCompleted.Total+7, after.LessonsCompleted.Total)
	require.Equal(t, before.LessonsCompleted.Week+7, after.LessonsCompleted.Week)
	require.Equal(t, before.PlaysWeek+7, after.PlaysWeek)

	missed := readLesson(t, router, token, lessons[0]).Questions[0]
	var found *hardGameQuestion
	for i, question := range after.Hardest {
		require.GreaterOrEqual(t, question.Asked, 5, "asked five times at least")
		require.Positive(t, question.Missed)
		if question.QuestionID == missed.ID {
			found = &after.Hardest[i]
		}
	}
	require.NotNil(t, found, "5 misses out of 6: %v", after.Hardest)
	require.Equal(t, 6, found.Asked)
	require.Equal(t, 5, found.Missed)
	require.Equal(t, lessons[0], found.LessonID)
	require.Equal(t, "Leçon 1", found.LessonTitle)
	require.Equal(t, code, found.Course)
	require.Equal(t, missed.Prompt["fr"], found.Prompt)
	require.LessOrEqual(t, len(after.Hardest), 10)

	// a week later, the week's numbers start again
	setGameClock(t, november(19, 10, 0))
	later := readGameStats(t, router, token)
	require.Zero(t, later.Players.Week)
	require.Zero(t, later.LessonsCompleted.Week)
	require.Equal(t, after.Players.Total, later.Players.Total)
}
