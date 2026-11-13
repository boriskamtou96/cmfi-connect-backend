package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// chooseLeaderboard is the member's answer to "appear in the leaderboard?".
func chooseLeaderboard(t *testing.T, router http.Handler, m member, in bool) gamePlayerResponse {
	response := send(t, router, http.MethodPut, m.Token, "/v1/games/me", map[string]any{"in_leaderboard": in})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[gamePlayerResponse](t, response)
}

func leaderboard(t *testing.T, router http.Handler, m member, query string) gameLeaderboardResponse {
	response := send(t, router, http.MethodGet, m.Token, "/v1/games/leaderboard"+query, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[gameLeaderboardResponse](t, response)
}

// among keeps the lines of [members] in the order of the leaderboard (other
// tests may have left players in the base).
func among(board gameLeaderboardResponse, members ...member) []gameLeaderboardEntry {
	var lines []gameLeaderboardEntry
	for _, entry := range board.Entries {
		for _, m := range members {
			if entry.Username == m.Username {
				lines = append(lines, entry)
			}
		}
	}
	return lines
}

func boardNames(entries []gameLeaderboardEntry) []string {
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Username)
	}
	return names
}

func TestTheLeaderboardListsThoseWhoChoseToAppear(t *testing.T) {
	router := newTestRouter(t)
	_, lessons := importSampleCourse(t, router, 1)
	setGameClock(t, november(12, 10, 0))
	yes, no, undecided := registerMember(t, router, nil), registerMember(t, router, nil), registerMember(t, router, nil)
	for _, m := range []member{yes, no, undecided} {
		playLesson(t, router, m.Token, lessons[0], "p", "JJJJJJJJ")
	}

	require.Nil(t, getPath(t, router, undecided.Token, "").Player.InLeaderboard)
	require.Equal(t, ptr(true), chooseLeaderboard(t, router, yes, true).InLeaderboard)
	require.Equal(t, ptr(false), chooseLeaderboard(t, router, no, false).InLeaderboard)

	board := leaderboard(t, router, yes, "")
	require.Equal(t, "week", board.Period)
	require.Equal(t, "all", board.Scope)
	lines := among(board, yes, no, undecided)
	require.Equal(t, []string{yes.Username}, boardNames(lines))
	require.Equal(t, 105, lines[0].Points)
	require.True(t, lines[0].Me)
	require.NotNil(t, board.Me)
	require.Equal(t, lines[0].Rank, board.Me.Rank)
	require.Equal(t, 105, board.Me.Points)
	require.Equal(t, ptr(board.Me.Rank), getPath(t, router, yes.Token, "").Player.WeekRank)

	// who said no still sees the others, and is not in it
	for _, m := range []member{no, undecided} {
		theirs := leaderboard(t, router, m, "?period=week&scope=all")
		require.Equal(t, []string{yes.Username}, boardNames(among(theirs, yes, no, undecided)))
		require.False(t, among(theirs, yes)[0].Me)
		require.Nil(t, theirs.Me)
		require.Nil(t, getPath(t, router, m.Token, "").Player.WeekRank)
	}
}

func TestATieGoesToWhoeverGotThereFirst(t *testing.T) {
	router := newTestRouter(t)
	_, lessons := importSampleCourse(t, router, 1)
	late, early, ahead := registerMember(t, router, nil), registerMember(t, router, nil), registerMember(t, router, nil)
	for _, m := range []member{late, early, ahead} {
		chooseLeaderboard(t, router, m, true)
	}
	setGameClock(t, november(12, 10, 0))
	playLesson(t, router, early.Token, lessons[0], "p", "JJFJJJJF") // 70
	setGameClock(t, november(12, 10, 5))
	playLesson(t, router, late.Token, lessons[0], "p", "JJFJJJJF")  // 70, later
	playLesson(t, router, ahead.Token, lessons[0], "p", "JJJJJJJJ") // 105

	board := leaderboard(t, router, late, "?period=all")
	lines := among(board, late, early, ahead)
	require.Equal(t, []string{ahead.Username, early.Username, late.Username}, boardNames(lines))
	require.Equal(t, lines[0].Rank+1, lines[1].Rank)
	require.Equal(t, lines[1].Rank+1, lines[2].Rank)
	require.Equal(t, lines[2].Rank, board.Me.Rank)
}

func TestTheWeekStartsOnMondayInDouala(t *testing.T) {
	router := newTestRouter(t)
	_, lessons := importSampleCourse(t, router, 2)
	m := registerMember(t, router, nil)
	chooseLeaderboard(t, router, m, true)

	// Sunday 15, 23:30 in Douala
	setGameClock(t, november(15, 22, 30))
	playLesson(t, router, m.Token, lessons[0], "sunday", "JJJJJJJJ")
	require.Len(t, among(leaderboard(t, router, m, "?period=week"), m), 1)

	// Monday 16: a new week
	setGameClock(t, november(16, 8, 0))
	week := leaderboard(t, router, m, "?period=week")
	require.Empty(t, among(week, m))
	require.Nil(t, week.Me)
	ever := leaderboard(t, router, m, "?period=all")
	require.Equal(t, 105, among(ever, m)[0].Points)

	playLesson(t, router, m.Token, lessons[1], "monday", "JJFJJJJF")
	require.Equal(t, 70, among(leaderboard(t, router, m, "?period=week"), m)[0].Points)
	require.Equal(t, 175, among(leaderboard(t, router, m, "?period=all"), m)[0].Points)
}

func TestBlockedMembersDoNotSeeEachOtherInTheLeaderboard(t *testing.T) {
	router := newTestRouter(t)
	_, lessons := importSampleCourse(t, router, 1)
	setGameClock(t, november(12, 10, 0))
	marie, boris, witness := registerMember(t, router, nil), registerMember(t, router, nil), registerMember(t, router, nil)
	for _, m := range []member{marie, boris, witness} {
		chooseLeaderboard(t, router, m, true)
		playLesson(t, router, m.Token, lessons[0], "p", "JJJJJJJJ")
	}
	require.Equal(t, http.StatusNoContent,
		send(t, router, http.MethodPost, marie.Token, "/v1/members/"+boris.Username+"/block", nil).Code)

	require.ElementsMatch(t, []string{marie.Username, witness.Username},
		boardNames(among(leaderboard(t, router, marie, ""), marie, boris, witness)))
	require.ElementsMatch(t, []string{boris.Username, witness.Username},
		boardNames(among(leaderboard(t, router, boris, ""), marie, boris, witness)))
	require.Len(t, among(leaderboard(t, router, witness, ""), marie, boris, witness), 3)
	// the rank does not count who is hidden
	theirs := leaderboard(t, router, boris, "")
	require.Equal(t, among(theirs, boris)[0].Rank, theirs.Me.Rank)
}

func TestMyCircleIsMyAuthoritiesAndMyDisciples(t *testing.T) {
	router := newTestRouter(t)
	_, lessons := importSampleCourse(t, router, 1)
	setGameClock(t, november(12, 10, 0))
	me, authority, disciple, stranger := registerMember(t, router, nil), registerMember(t, router, nil),
		registerMember(t, router, nil), registerMember(t, router, nil)
	for _, m := range []member{me, authority, disciple, stranger} {
		chooseLeaderboard(t, router, m, true)
		playLesson(t, router, m.Token, lessons[0], "p", "JJJJJJJJ")
	}
	chooseAuthority(t, router, me, authority, false)
	chooseAuthority(t, router, disciple, me, true)

	circle := leaderboard(t, router, me, "?scope=circle&period=all")
	require.Equal(t, "circle", circle.Scope)
	require.ElementsMatch(t, []string{me.Username, authority.Username, disciple.Username}, boardNames(circle.Entries))
	require.Equal(t, among(circle, me)[0].Rank, circle.Me.Rank)
	require.LessOrEqual(t, circle.Me.Rank, 3)

	// without anyone around: alone in their circle
	require.Equal(t, []string{stranger.Username}, boardNames(leaderboard(t, router, stranger, "?scope=circle").Entries))
}

func TestTheDailyGoalIsOneOfThree(t *testing.T) {
	router := newTestRouter(t)
	m := registerMember(t, router, nil)

	set := send(t, router, http.MethodPut, m.Token, "/v1/games/me", map[string]any{"daily_goal": 100})
	require.Equal(t, http.StatusOK, set.Code, set.Body.String())
	require.Equal(t, 100, decode[gamePlayerResponse](t, set).DailyGoal)

	require.Equal(t, http.StatusBadRequest,
		send(t, router, http.MethodPut, m.Token, "/v1/games/me", map[string]any{"daily_goal": 40}).Code)
	// one setting does not undo the other
	player := chooseLeaderboard(t, router, m, true)
	require.Equal(t, 100, player.DailyGoal)
	unchanged := decode[gamePlayerResponse](t, send(t, router, http.MethodPut, m.Token, "/v1/games/me", map[string]any{}))
	require.Equal(t, ptr(true), unchanged.InLeaderboard)
	require.Equal(t, 100, unchanged.DailyGoal)

	for _, query := range []string{"?period=month", "?scope=church"} {
		require.Equal(t, http.StatusBadRequest,
			send(t, router, http.MethodGet, m.Token, "/v1/games/leaderboard"+query, nil).Code, query)
	}
}
