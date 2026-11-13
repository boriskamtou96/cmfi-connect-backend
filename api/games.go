package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/games"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// The games (the tab in the middle of the app): courses made of levels, each
// level of short lessons of questions. A lesson opens once the one before it
// is done. The server counts the points; the days and the weeks of the games
// are those of Douala (internal/games).

// gameClock gives the time of the games (tests set the day).
var gameClock = time.Now

const (
	lessonDone   = "done"
	lessonOpen   = "open"
	lessonLocked = "locked"
)

var errGameLessonNotFound = errors.New("lesson not found")

type gameCourseResponse struct {
	Code  string `json:"code"`
	Title string `json:"title"`
}

// gamePlayerResponse is the member as a player: what the top of the tab shows.
type gamePlayerResponse struct {
	TotalPoints int `json:"total_points"`
	// the level of the player, the points where it began and those of the next
	Level       int `json:"level"`
	LevelFloor  int `json:"level_floor"`
	NextLevelAt int `json:"next_level_at"`
	// the days played in a row (0 once broken)
	StreakDays  int  `json:"streak_days"`
	PlayedToday bool `json:"played_today"`
	DailyGoal   int  `json:"daily_goal"`
	PointsToday int  `json:"points_today"`
	// null until the member chose to appear in the leaderboard or not
	InLeaderboard *bool `json:"in_leaderboard"`
	// their rank of the week among all, null when they are not in it
	WeekRank  *int `json:"week_rank"`
	DailyDone bool `json:"daily_done"`
}

type gameLessonSummary struct {
	ID       int64  `json:"id"`
	Position int32  `json:"position"`
	Title    string `json:"title"`
	// done, open or locked
	State string `json:"state"`
	Stars int    `json:"stars"`
}

type gameLevelResponse struct {
	ID          int64               `json:"id"`
	Position    int32               `json:"position"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Color       string              `json:"color"`
	Lessons     []gameLessonSummary `json:"lessons"`
}

type gamePathResponse struct {
	Courses []gameCourseResponse `json:"courses"`
	// the course shown
	Course string              `json:"course"`
	Player gamePlayerResponse  `json:"player"`
	Levels []gameLevelResponse `json:"levels"`
}

type gameChoiceResponse struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type gameQuestionResponse struct {
	ID      int64                `json:"id"`
	Kind    string               `json:"kind"`
	Prompt  string               `json:"prompt"`
	Choices []gameChoiceResponse `json:"choices"`
	// the id of the right choice: the phone explains each answer without
	// waiting for the network (the points are still counted here)
	Answer      string `json:"answer"`
	Explanation string `json:"explanation"`
	Source      string `json:"source"`
}

type gameLessonResponse struct {
	ID         int64                  `json:"id"`
	Title      string                 `json:"title"`
	LevelTitle string                 `json:"level_title"`
	Course     string                 `json:"course"`
	Questions  []gameQuestionResponse `json:"questions"`
}

// textIn reads a translated text ({"fr": "…", "en": "…"}) in [lang], in
// French when that language is missing.
func textIn(raw json.RawMessage, lang string) string {
	var text gameText
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	if value := text[lang]; value != "" {
		return value
	}
	return text[gameDefaultLang]
}

// gameLang is the language asked for (?lang=), French by default.
func gameLang(c *gin.Context) string {
	if lang := c.Query("lang"); lang != "" {
		return lang
	}
	return gameDefaultLang
}

func dateOf(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	year, month, day := value.Time.Date()
	date := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return &date
}

func sameDay(date *time.Time, day time.Time) bool { return date != nil && date.Equal(day) }

func streakOf(player db.GamePlayer) games.Streak {
	return games.Streak{
		Days:         int(player.StreakDays),
		LastPlayedOn: dateOf(player.LastPlayedOn),
		GraceUsedOn:  dateOf(player.GraceUsedOn),
	}
}

// gamePlayer is member [userID] as a player at [now]; a member who never
// played has everything at zero.
func (s *Server) gamePlayer(c *gin.Context, userID int64, now time.Time) (gamePlayerResponse, error) {
	player, err := s.store.GetGamePlayer(c, userID)
	if errors.Is(err, sql.ErrNoRows) {
		player, err = db.GamePlayer{UserID: userID, DailyGoal: 50}, nil
	}
	if err != nil {
		return gamePlayerResponse{}, err
	}
	today := games.GameDay(now)
	level, floor, next := games.PlayerLevel(int(player.TotalPoints))
	response := gamePlayerResponse{
		TotalPoints: int(player.TotalPoints),
		Level:       level,
		LevelFloor:  floor,
		NextLevelAt: next,
		StreakDays:  streakOf(player).ShownOn(today),
		PlayedToday: sameDay(dateOf(player.LastPlayedOn), today),
		DailyGoal:   int(player.DailyGoal),
		DailyDone:   sameDay(dateOf(player.DailyDoneOn), today),
	}
	if player.InLeaderboard.Valid {
		response.InLeaderboard = &player.InLeaderboard.Bool
	}

	points, err := s.store.SumGamePointsSince(c, db.SumGamePointsSinceParams{UserID: userID, Since: games.DayStart(now)})
	if err != nil {
		return gamePlayerResponse{}, err
	}
	response.PointsToday = int(points)

	if player.InLeaderboard.Valid && player.InLeaderboard.Bool {
		since := sql.NullTime{Time: games.WeekStart(now), Valid: true}
		rank, _, err := s.gameRank(c, userID, since, nil)
		if err != nil {
			return gamePlayerResponse{}, err
		}
		response.WeekRank = rank
	}
	return response, nil
}

// gameRank is the rank and the points of [userID] over a period ([since] not
// valid: since ever) among [ids] (nil: every member); no rank without points.
func (s *Server) gameRank(c *gin.Context, userID int64, since sql.NullTime, ids []int64) (*int, int, error) {
	standing, err := s.store.GetGameStanding(c, db.GetGameStandingParams{UserID: userID, Since: since})
	if err != nil {
		return nil, 0, err
	}
	if standing.Points <= 0 {
		return nil, 0, nil
	}
	ahead, err := s.store.CountGameLeadersAhead(c, db.CountGameLeadersAheadParams{
		Since: since, ViewerID: userID, Ids: ids, Points: standing.Points, ReachedAt: standing.ReachedAt,
	})
	if err != nil {
		return nil, 0, err
	}
	rank := int(ahead) + 1
	return &rank, int(standing.Points), nil
}

// lessonStates gives the state of each lesson of a path, in its order: done,
// else open when it is the first one or the one before it is done.
func lessonStates(rows []db.ListGameLessonsOfCourseRow) map[int64]string {
	states := make(map[int64]string, len(rows))
	previousDone := true
	for _, row := range rows {
		switch {
		case row.Done:
			states[row.ID] = lessonDone
		case previousDone:
			states[row.ID] = lessonOpen
		default:
			states[row.ID] = lessonLocked
		}
		previousDone = row.Done
	}
	return states
}

// GET /v1/games/path?course=&lang=: the courses, and the path of one of them
// (the first one by default) as the member walks it.
func (s *Server) getGamePath(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	lang := gameLang(c)

	courses, err := s.store.ListGameCourses(c)
	if err != nil {
		internalServerError(c, err)
		return
	}
	response := gamePathResponse{Courses: make([]gameCourseResponse, 0, len(courses)), Levels: []gameLevelResponse{}}
	var courseID int64
	code := c.Query("course")
	for _, course := range courses {
		response.Courses = append(response.Courses, gameCourseResponse{Code: course.Code, Title: textIn(course.Title, lang)})
		if courseID == 0 && (code == "" || code == course.Code) {
			courseID, response.Course = course.ID, course.Code
		}
	}
	if courseID == 0 {
		resourceNotFoundError(c, "course")
		return
	}

	levels, err := s.store.ListGameLevelsOfCourse(c, courseID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	lessons, err := s.store.ListGameLessonsOfCourse(c, db.ListGameLessonsOfCourseParams{UserID: authUser.ID, CourseID: courseID})
	if err != nil {
		internalServerError(c, err)
		return
	}
	states := lessonStates(lessons)
	for _, level := range levels {
		item := gameLevelResponse{
			ID: level.ID, Position: level.Position, Title: textIn(level.Title, lang),
			Description: textIn(level.Description, lang), Color: level.Color, Lessons: []gameLessonSummary{},
		}
		for _, lesson := range lessons {
			if lesson.LevelID == level.ID {
				item.Lessons = append(item.Lessons, gameLessonSummary{
					ID: lesson.ID, Position: lesson.Position, Title: textIn(lesson.Title, lang),
					State: states[lesson.ID], Stars: int(lesson.Stars),
				})
			}
		}
		response.Levels = append(response.Levels, item)
	}

	response.Player, err = s.gamePlayer(c, authUser.ID, gameClock())
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, response)
}

// gameLessonFor is a published lesson with its state for [userID], and the
// lessons of its course in the order of the path. errGameLessonNotFound
// when the members cannot see it.
func (s *Server) gameLessonFor(c *gin.Context, userID, lessonID int64) (db.GetGameLessonRow, string, []db.ListGameLessonsOfCourseRow, error) {
	lesson, err := s.store.GetGameLesson(c, lessonID)
	if errors.Is(err, sql.ErrNoRows) {
		return lesson, "", nil, errGameLessonNotFound
	}
	if err != nil {
		return lesson, "", nil, err
	}
	rows, err := s.store.ListGameLessonsOfCourse(c, db.ListGameLessonsOfCourseParams{UserID: userID, CourseID: lesson.CourseID})
	if err != nil {
		return lesson, "", nil, err
	}
	return lesson, lessonStates(rows)[lessonID], rows, nil
}

func lessonLockedError(c *gin.Context) {
	c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "LESSON_LOCKED",
		fmt.Errorf("this lesson opens once the one before it is done")))
}

// gameLessonID reads the :id of the route; false once it answered 400.
func gameLessonID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		badRequestError(c, fmt.Errorf("the id of the lesson is a number"))
		return 0, false
	}
	return id, true
}

func toGameQuestions(rows []gameQuestionRow, lang string) []gameQuestionResponse {
	questions := make([]gameQuestionResponse, 0, len(rows))
	for _, row := range rows {
		var choices []importGameChoice
		_ = json.Unmarshal(row.Choices, &choices)
		question := gameQuestionResponse{
			ID: row.ID, Kind: row.Kind, Prompt: textIn(row.Prompt, lang), Choices: make([]gameChoiceResponse, 0, len(choices)),
			Answer: row.Answer, Explanation: textIn(row.Explanation, lang), Source: row.Source,
		}
		for _, choice := range choices {
			text, _ := json.Marshal(choice.Text)
			question.Choices = append(question.Choices, gameChoiceResponse{ID: choice.ID, Text: textIn(text, lang)})
		}
		questions = append(questions, question)
	}
	return questions
}

// gameQuestionRow is a question as the queries give it.
type gameQuestionRow = db.ListGameQuestionsOfLessonRow

// GET /v1/games/lessons/:id?lang=: the questions of a lesson open to the
// member.
func (s *Server) getGameLesson(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	id, ok := gameLessonID(c)
	if !ok {
		return
	}
	lesson, state, _, err := s.gameLessonFor(c, authUser.ID, id)
	if errors.Is(err, errGameLessonNotFound) {
		resourceNotFoundError(c, "lesson")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	if state == lessonLocked {
		lessonLockedError(c)
		return
	}
	rows, err := s.store.ListGameQuestionsOfLesson(c, id)
	if err != nil {
		internalServerError(c, err)
		return
	}
	lang := gameLang(c)
	apiResponse(c, http.StatusOK, gameLessonResponse{
		ID: lesson.ID, Title: textIn(lesson.Title, lang), LevelTitle: textIn(lesson.LevelTitle, lang),
		Course: lesson.CourseCode, Questions: toGameQuestions(rows, lang),
	})
}

// gameResultResponse is what a play gave. (Filled by submitGameLesson.)
type gameResultResponse struct {
	Points   int `json:"points"`
	FirstTry int `json:"first_try"`
	Total    int `json:"total"`
	Stars    int `json:"stars"`
	// the best stars of the lesson, this play included
	BestStars  int  `json:"best_stars"`
	Perfect    bool `json:"perfect"`
	ComboBonus int  `json:"combo_bonus"`
	// the lesson was already done: few points, capped by day
	Replay  bool `json:"replay"`
	LevelUp bool `json:"level_up"`
	// the lesson after this one on the path, null at its end
	NextLessonID *int64 `json:"next_lesson_id"`
	// this play finished the lessons of its level
	LevelCompleted bool               `json:"level_completed"`
	Player         gamePlayerResponse `json:"player"`
}

type gameAnswerRequest struct {
	QuestionID int64  `json:"question_id"`
	Choice     string `json:"choice"`
	Attempt    int    `json:"attempt"`
}

// gamePlayRequest is a play sent by the phone. client_id names it: sent
// again (a weak network), it counts once.
type gamePlayRequest struct {
	ClientID string              `json:"client_id"`
	Answers  []gameAnswerRequest `json:"answers"`
}

var errNoGameAnswer = errors.New("no answer is about a question of this lesson")

// read checks a play and gives its answers; false once it answered 400.
func (r gamePlayRequest) read(c *gin.Context) ([]games.Answer, bool) {
	if id := strings.TrimSpace(r.ClientID); id == "" || len(r.ClientID) > 64 {
		badRequestError(c, fmt.Errorf("client_id: 1 to 64 characters"))
		return nil, false
	}
	if len(r.Answers) == 0 || len(r.Answers) > 200 {
		badRequestError(c, fmt.Errorf("answers: at least one, 200 at most"))
		return nil, false
	}
	answers := make([]games.Answer, 0, len(r.Answers))
	for _, answer := range r.Answers {
		answers = append(answers, games.Answer{QuestionID: answer.QuestionID, Choice: answer.Choice, Attempt: answer.Attempt})
	}
	return answers, true
}

// firstAnswers gives the questions of [key] answered in a play, and those
// whose first answer was wrong.
func firstAnswers(answers []games.Answer, key map[int64]string) (asked, missed []int64) {
	seen := map[int64]bool{}
	asked, missed = []int64{}, []int64{}
	for _, answer := range answers {
		right, known := key[answer.QuestionID]
		if !known || seen[answer.QuestionID] {
			continue
		}
		seen[answer.QuestionID] = true
		asked = append(asked, answer.QuestionID)
		if answer.Choice != right {
			missed = append(missed, answer.QuestionID)
		}
	}
	return asked, missed
}

func nullDate(date *time.Time) sql.NullTime {
	if date == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *date, Valid: true}
}

// POST /v1/games/lessons/:id/results: a lesson played. The server counts:
// the points and the stars, the best play of the lesson, the total, the
// level and the streak of the player. The same play sent twice counts once.
func (s *Server) submitGameLesson(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	id, ok := gameLessonID(c)
	if !ok {
		return
	}
	var request gamePlayRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequestError(c, err)
		return
	}
	answers, ok := request.read(c)
	if !ok {
		return
	}
	lesson, state, _, err := s.gameLessonFor(c, authUser.ID, id)
	if errors.Is(err, errGameLessonNotFound) {
		resourceNotFoundError(c, "lesson")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	if state == lessonLocked {
		lessonLockedError(c)
		return
	}
	questions, err := s.store.ListGameQuestionsOfLesson(c, id)
	if err != nil {
		internalServerError(c, err)
		return
	}
	key := make(map[int64]string, len(questions))
	for _, question := range questions {
		key[question.ID] = question.Answer
	}

	now := gameClock()
	var play db.GamePlay
	err = s.store.WithGamePlayerTx(c, authUser.ID, func(q *db.Queries, player db.GamePlayer) error {
		var err error
		play, err = q.GetGamePlayByClientID(c, db.GetGamePlayByClientIDParams{UserID: authUser.ID, ClientID: request.ClientID})
		if err == nil {
			// already received
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		replay, err := q.HasGameLessonResult(c, db.HasGameLessonResultParams{UserID: authUser.ID, LessonID: id})
		if err != nil {
			return err
		}
		kind := "lesson"
		var score games.Score
		var counted bool
		if replay {
			kind = "replay"
			today, err := q.SumGameReplayPointsSince(c, db.SumGameReplayPointsSinceParams{UserID: authUser.ID, Since: games.DayStart(now)})
			if err != nil {
				return err
			}
			score, counted = games.ScoreReplay(answers, key, int(today))
		} else {
			score, counted = games.ScoreLesson(answers, key)
		}
		if !counted {
			return errNoGameAnswer
		}

		before, _, _ := games.PlayerLevel(int(player.TotalPoints))
		total := int(player.TotalPoints) + score.Points
		after, _, _ := games.PlayerLevel(total)
		play, err = q.CreateGamePlay(c, db.CreateGamePlayParams{
			UserID: authUser.ID, LessonID: sql.NullInt64{Int64: id, Valid: true}, Kind: kind, ClientID: request.ClientID,
			FirstTry: int16(score.FirstTry), Total: int16(score.Total), Stars: int16(score.Stars),
			Points: int32(score.Points), ComboBonus: int32(score.ComboBonus), LevelUp: after > before, PlayedAt: now,
		})
		if err != nil {
			return err
		}
		if !replay {
			// what the members miss when they meet a question: for the back-office
			asked, missed := firstAnswers(answers, key)
			if err := q.BumpGameQuestionStats(c, db.BumpGameQuestionStatsParams{QuestionIds: asked, MissedIds: missed}); err != nil {
				return err
			}
		}
		if _, err := q.UpsertGameLessonResult(c, db.UpsertGameLessonResultParams{
			UserID: authUser.ID, LessonID: id, Stars: int16(score.Stars), FirstTry: int16(score.FirstTry), PlayedAt: now,
		}); err != nil {
			return err
		}
		streak := streakOf(player).After(games.GameDay(now))
		return q.SaveGamePlayerProgress(c, db.SaveGamePlayerProgressParams{
			UserID: authUser.ID, TotalPoints: int32(total), StreakDays: int32(streak.Days),
			LastPlayedOn: nullDate(streak.LastPlayedOn), GraceUsedOn: nullDate(streak.GraceUsedOn),
			DailyDoneOn: player.DailyDoneOn,
		})
	})
	if errors.Is(err, errNoGameAnswer) {
		badRequestError(c, err)
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}

	response := gameResultResponse{
		Points: int(play.Points), FirstTry: int(play.FirstTry), Total: int(play.Total), Stars: int(play.Stars),
		Perfect: play.Total > 0 && play.FirstTry == play.Total, ComboBonus: int(play.ComboBonus),
		Replay: play.Kind == "replay", LevelUp: play.LevelUp,
	}
	best, err := s.store.GetGameLessonBestStars(c, db.GetGameLessonBestStarsParams{UserID: authUser.ID, LessonID: id})
	if err != nil {
		internalServerError(c, err)
		return
	}
	response.BestStars = int(best)

	// the path after the play: the next lesson, and the level finished or not
	rows, err := s.store.ListGameLessonsOfCourse(c, db.ListGameLessonsOfCourseParams{UserID: authUser.ID, CourseID: lesson.CourseID})
	if err != nil {
		internalServerError(c, err)
		return
	}
	levelDone := true
	var levelID int64
	for i, row := range rows {
		if row.ID == id {
			levelID = row.LevelID
			if i+1 < len(rows) {
				response.NextLessonID = &rows[i+1].ID
			}
		}
	}
	for _, row := range rows {
		if row.LevelID == levelID && !row.Done {
			levelDone = false
		}
	}
	response.LevelCompleted = levelDone && !response.Replay

	response.Player, err = s.gamePlayer(c, authUser.ID, now)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, response)
}
