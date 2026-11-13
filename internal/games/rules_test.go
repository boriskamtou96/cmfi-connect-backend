package games

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// the right choice is always "a"
func keyOf(n int) map[int64]string {
	key := map[int64]string{}
	for id := int64(1); id <= int64(n); id++ {
		key[id] = "a"
	}
	return key
}

// answers plays the questions 1..n in order: 'J' right at once, 'F' wrong
// first then right on the second pass.
func answers(firstTries string) []Answer {
	var list, second []Answer
	for i, mark := range firstTries {
		id := int64(i + 1)
		if mark == 'J' {
			list = append(list, Answer{QuestionID: id, Choice: "a", Attempt: 1})
			continue
		}
		list = append(list, Answer{QuestionID: id, Choice: "b", Attempt: 1})
		second = append(second, Answer{QuestionID: id, Choice: "a", Attempt: 2})
	}
	return append(list, second...)
}

func TestALessonWithoutAMistakeGivesEverything(t *testing.T) {
	score, ok := ScoreLesson(answers("JJJJJJJJ"), keyOf(8))
	require.True(t, ok)
	require.Equal(t, Score{FirstTry: 8, Total: 8, Stars: 3, Points: 105, ComboBonus: 5, Perfect: true}, score)
}

func TestMistakesCaughtOnTheSecondPassGiveHalf(t *testing.T) {
	score, ok := ScoreLesson(answers("JJFJJJJF"), keyOf(8))
	require.True(t, ok)
	require.Equal(t, Score{FirstTry: 6, Total: 8, Stars: 2, Points: 70}, score)
}

func TestFiveRightInARowGiveABonus(t *testing.T) {
	score, ok := ScoreLesson(answers("JJJJJFFF"), keyOf(8))
	require.True(t, ok)
	require.Equal(t, Score{FirstTry: 5, Total: 8, Stars: 1, Points: 70, ComboBonus: 5}, score)
}

func TestAMistakeNeverCaughtGivesNothing(t *testing.T) {
	list := answers("JJJJJJJ")
	list = append(list, Answer{QuestionID: 8, Choice: "b", Attempt: 1}, Answer{QuestionID: 8, Choice: "c", Attempt: 2})
	score, ok := ScoreLesson(list, keyOf(8))
	require.True(t, ok)
	require.Equal(t, 7, score.FirstTry)
	require.Equal(t, 75, score.Points) // 70 + the bonus of 5 in a row
	require.False(t, score.Perfect)
}

func TestAnswersToQuestionsGoneAreIgnored(t *testing.T) {
	// the lesson lost its questions 7 and 8 since it was downloaded
	score, ok := ScoreLesson(answers("JJJJJJFF"), keyOf(6))
	require.True(t, ok)
	require.Equal(t, Score{FirstTry: 6, Total: 6, Stars: 3, Points: 85, ComboBonus: 5, Perfect: true}, score)

	_, ok = ScoreLesson([]Answer{{QuestionID: 99, Choice: "a", Attempt: 1}}, keyOf(8))
	require.False(t, ok)
	_, ok = ScoreLesson(nil, keyOf(8))
	require.False(t, ok)
}

func TestALessonPlayedAgainGivesLittleAndIsCappedByDay(t *testing.T) {
	score, ok := ScoreReplay(answers("JJJJJJJJ"), keyOf(8), 0)
	require.True(t, ok)
	require.Equal(t, 16, score.Points)
	require.Equal(t, 3, score.Stars)
	require.Zero(t, score.ComboBonus)

	score, _ = ScoreReplay(answers("JJJJJJJJ"), keyOf(8), 10)
	require.Equal(t, 10, score.Points)
	score, _ = ScoreReplay(answers("JJJJJJJJ"), keyOf(8), 20)
	require.Zero(t, score.Points)
	score, _ = ScoreReplay(answers("JJJJJJJJ"), keyOf(8), 35)
	require.Zero(t, score.Points)
}

func TestTheStars(t *testing.T) {
	require.Equal(t, 3, Stars(8, 8))
	require.Equal(t, 2, Stars(6, 8))
	require.Equal(t, 1, Stars(5, 8))
	require.Equal(t, 2, Stars(3, 4))
	require.Equal(t, 1, Stars(0, 8))
}

func TestThePlayerLevel(t *testing.T) {
	for _, c := range []struct{ total, level, floor, next int }{
		{0, 1, 0, 200}, {199, 1, 0, 200}, {200, 2, 200, 600}, {1199, 3, 600, 1200}, {1200, 4, 1200, 2000},
	} {
		level, floor, next := PlayerLevel(c.total)
		require.Equal(t, []int{c.level, c.floor, c.next}, []int{level, floor, next}, "total %d", c.total)
	}
}

func day(d int) time.Time { return time.Date(2026, time.November, d, 0, 0, 0, 0, time.UTC) }

func TestTheDayAndTheWeekAreThoseOfDouala(t *testing.T) {
	late := time.Date(2026, time.November, 12, 23, 30, 0, 0, time.UTC)
	require.Equal(t, day(13), GameDay(late))
	require.Equal(t, time.Date(2026, time.November, 12, 23, 0, 0, 0, time.UTC), DayStart(late).UTC())

	// Sunday 15 at 23:30 UTC is already Monday 16 in Douala
	sunday := time.Date(2026, time.November, 15, 23, 30, 0, 0, time.UTC)
	require.Equal(t, time.Date(2026, time.November, 15, 23, 0, 0, 0, time.UTC), WeekStart(sunday).UTC())
	// Thursday 12 at noon: the week began on Monday 9 at 00:00 in Douala
	require.Equal(t, time.Date(2026, time.November, 8, 23, 0, 0, 0, time.UTC),
		WeekStart(time.Date(2026, time.November, 12, 12, 0, 0, 0, time.UTC)).UTC())
}

func TestTheStreakGrowsDayAfterDay(t *testing.T) {
	streak := Streak{}.After(day(9))
	require.Equal(t, 1, streak.Days)
	streak = streak.After(day(10))
	require.Equal(t, 2, streak.Days)
	// twice the same day
	require.Equal(t, 2, streak.After(day(10)).Days)
	require.Equal(t, 2, streak.ShownOn(day(10)))
	require.Equal(t, 2, streak.ShownOn(day(11)))
}

func TestOneMissedDayAWeekIsForgiven(t *testing.T) {
	// Monday 9, Tuesday 10, nothing on Wednesday 11, Thursday 12
	streak := Streak{}.After(day(9)).After(day(10))
	require.Equal(t, 2, streak.ShownOn(day(12)), "the grace still protects it")
	streak = streak.After(day(12))
	require.Equal(t, 3, streak.Days)
	require.Equal(t, day(11), *streak.GraceUsedOn)

	// nothing on Friday 13 either: the grace of the week is spent
	require.Zero(t, streak.ShownOn(day(14)))
	streak = streak.After(day(14))
	require.Equal(t, 1, streak.Days)

	// the next week has its own day of grace (Monday 16 missed)
	streak = streak.After(day(15)).After(day(17))
	require.Equal(t, 3, streak.Days)
	require.Equal(t, day(16), *streak.GraceUsedOn)
}

func TestTwoMissedDaysBreakTheStreak(t *testing.T) {
	streak := Streak{}.After(day(9)).After(day(10))
	require.Zero(t, streak.ShownOn(day(13)))
	require.Equal(t, 1, streak.After(day(13)).Days)
	require.Zero(t, Streak{}.ShownOn(day(13)))
}
