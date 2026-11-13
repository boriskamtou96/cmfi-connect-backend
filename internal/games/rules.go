// Package games holds the rules of the games: what a lesson played is worth,
// the level of a player, their streak of days. Pure functions: the API reads
// and writes, this decides.
package games

import "time"

const (
	// a right answer at once, or on the second pass of the question
	PointsFirstTry  = 10
	PointsSecondTry = 5
	// a lesson without a mistake
	PointsPerfect = 20
	// every 5 right answers in a row
	ComboLength = 5
	PointsCombo = 5
	// a lesson already finished, played again: little, and capped by day
	PointsReplay      = 2
	ReplayPointsByDay = 20
	// the challenge of the day
	PointsDaily = 15
)

// douala is the time of the games (UTC+1 all year): their days and weeks.
var douala = time.FixedZone("WAT", 3600)

// Answer is one answer of a play, in the order they were given: a question
// answered wrong comes back once ([Attempt] 2).
type Answer struct {
	QuestionID int64
	Choice     string
	Attempt    int
}

// Score is what a play is worth.
type Score struct {
	// questions answered right at once, out of those played
	FirstTry, Total int
	Stars           int
	// all the points of the play, bonuses included
	Points     int
	ComboBonus int
	Perfect    bool
}

// tally reads the answers against [key] (the right choice of each question
// still in the lesson; answers to other questions are ignored): the questions
// played, those right at once, those caught later, and the bonus of the rows
// of right answers.
func tally(answers []Answer, key map[int64]string) (total, firstTry, caught, combo int) {
	first := map[int64]bool{}
	later := map[int64]bool{}
	run := 0
	for _, answer := range answers {
		right, known := key[answer.QuestionID]
		if !known {
			continue
		}
		ok := answer.Choice == right
		if _, seen := first[answer.QuestionID]; seen {
			later[answer.QuestionID] = later[answer.QuestionID] || ok
			continue
		}
		first[answer.QuestionID] = ok
		if ok {
			run++
			if run%ComboLength == 0 {
				combo += PointsCombo
			}
		} else {
			run = 0
		}
	}
	for id, ok := range first {
		total++
		switch {
		case ok:
			firstTry++
		case later[id]:
			caught++
		}
	}
	return total, firstTry, caught, combo
}

// ScoreLesson counts a lesson played for the first time. False when no answer
// is about a question of [key].
func ScoreLesson(answers []Answer, key map[int64]string) (Score, bool) {
	total, firstTry, caught, combo := tally(answers, key)
	if total == 0 {
		return Score{}, false
	}
	score := Score{
		FirstTry:   firstTry,
		Total:      total,
		Stars:      Stars(firstTry, total),
		ComboBonus: combo,
		Perfect:    firstTry == total,
	}
	score.Points = firstTry*PointsFirstTry + caught*PointsSecondTry + combo
	if score.Perfect {
		score.Points += PointsPerfect
	}
	return score, true
}

// ScoreReplay counts a lesson played again: no bonus, and never more than
// [ReplayPointsByDay] a day ([alreadyToday]: what the replays of the day gave).
func ScoreReplay(answers []Answer, key map[int64]string, alreadyToday int) (Score, bool) {
	total, firstTry, _, _ := tally(answers, key)
	if total == 0 {
		return Score{}, false
	}
	points := min(firstTry*PointsReplay, max(0, ReplayPointsByDay-alreadyToday))
	return Score{
		FirstTry: firstTry,
		Total:    total,
		Stars:    Stars(firstTry, total),
		Points:   points,
		Perfect:  firstTry == total,
	}, true
}

// Stars: 3 without a mistake, 2 from three right at once out of four, else 1.
func Stars(firstTry, total int) int {
	switch {
	case total > 0 && firstTry >= total:
		return 3
	case firstTry*4 >= total*3 && total > 0:
		return 2
	default:
		return 1
	}
}

// PlayerLevel is the level a total of points gives, the points where it
// began and those of the next one: level n from 100 × n × (n − 1).
func PlayerLevel(total int) (level, floor, next int) {
	level = 1
	for 100*(level+1)*level <= total {
		level++
	}
	return level, 100 * level * (level - 1), 100 * (level + 1) * level
}

// GameDay is the calendar day of [t] in Douala, as a date (midnight UTC, the
// way DATE columns are read).
func GameDay(t time.Time) time.Time {
	year, month, day := t.In(douala).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// DayStart is the instant the day of [t] began in Douala.
func DayStart(t time.Time) time.Time {
	year, month, day := t.In(douala).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, douala)
}

// WeekStart is the instant the week of [t] began in Douala: Monday, 00:00.
func WeekStart(t time.Time) time.Time {
	start := DayStart(t)
	return start.AddDate(0, 0, -daysSinceMonday(start.Weekday()))
}

func daysSinceMonday(weekday time.Weekday) int { return (int(weekday) + 6) % 7 }

// monday is the Monday of the week of a date.
func monday(date time.Time) time.Time {
	return date.AddDate(0, 0, -daysSinceMonday(date.Weekday()))
}

func daysBetween(from, to time.Time) int {
	return int(to.Sub(from).Hours() / 24)
}

// Streak is the days in a row a member played. Dates are game days
// ([GameDay]). One missed day a week is forgiven: the day of grace.
type Streak struct {
	Days         int
	LastPlayedOn *time.Time
	GraceUsedOn  *time.Time
}

// graceCovers says whether the day of grace of its week is still free for
// the missed day [missed].
func (s Streak) graceCovers(missed time.Time) bool {
	return s.GraceUsedOn == nil || !monday(*s.GraceUsedOn).Equal(monday(missed))
}

// After is the streak once a play is finished on [day].
func (s Streak) After(day time.Time) Streak {
	next := Streak{Days: 1, LastPlayedOn: &day, GraceUsedOn: s.GraceUsedOn}
	if s.LastPlayedOn == nil {
		return next
	}
	switch gap := daysBetween(*s.LastPlayedOn, day); {
	case gap <= 0:
		// the same day (or a clock set back): nothing changes
		return s
	case gap == 1:
		next.Days = s.Days + 1
	case gap == 2:
		missed := s.LastPlayedOn.AddDate(0, 0, 1)
		if s.graceCovers(missed) {
			next.Days = s.Days + 1
			next.GraceUsedOn = &missed
		}
	}
	return next
}

// ShownOn is the streak to show on [day]: 0 once it is broken, even if the
// member has not played since.
func (s Streak) ShownOn(day time.Time) int {
	if s.LastPlayedOn == nil {
		return 0
	}
	switch gap := daysBetween(*s.LastPlayedOn, day); {
	case gap <= 1:
		return s.Days
	case gap == 2 && s.graceCovers(s.LastPlayedOn.AddDate(0, 0, 1)):
		return s.Days
	default:
		return 0
	}
}
