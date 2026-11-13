package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
)

// The games in the back-office: the admins write the courses piece by piece.
// Ids stay (an import replaces a whole course, and is refused once members
// played it). Every write answers with the parent whole: the course for its
// levels and lessons, the lesson for its questions.

// gameLanguages are the languages of the app, French first.
var gameLanguages = map[string]bool{"fr": true, "en": true, "de": true, "es": true, "ar": true, "zh": true, "hi": true}

const (
	// a lesson is played only with this many questions (spec §6)
	gameLessonQuestions  = 8
	gameMaxChoices       = 6
	gameTitleLength      = 120
	gameDescription      = 300
	gamePromptLength     = 500
	gameChoiceLength     = 200
	gameExplanationLimit = 1000
)

type adminGameCourseSummary struct {
	Code      string   `json:"code"`
	Position  int      `json:"position"`
	Title     gameText `json:"title"`
	Published bool     `json:"published"`
	Levels    int      `json:"levels"`
	Lessons   int      `json:"lessons"`
	Questions int      `json:"questions"`
}

type adminGameLessonSummary struct {
	ID        int64    `json:"id"`
	Position  int      `json:"position"`
	Title     gameText `json:"title"`
	Published bool     `json:"published"`
	Questions int      `json:"questions"`
	// the members who finished it
	Players int `json:"players"`
}

type adminGameLevel struct {
	ID          int64                    `json:"id"`
	Position    int                      `json:"position"`
	Title       gameText                 `json:"title"`
	Description gameText                 `json:"description"`
	Color       string                   `json:"color"`
	Published   bool                     `json:"published"`
	Lessons     []adminGameLessonSummary `json:"lessons"`
}

type adminGameCourse struct {
	Code      string           `json:"code"`
	Position  int              `json:"position"`
	Title     gameText         `json:"title"`
	Published bool             `json:"published"`
	Levels    []adminGameLevel `json:"levels"`
}

type adminGameQuestion struct {
	ID          int64              `json:"id"`
	Position    int                `json:"position"`
	Kind        string             `json:"kind"`
	Prompt      gameText           `json:"prompt"`
	Choices     []importGameChoice `json:"choices"`
	Answer      string             `json:"answer"`
	Explanation gameText           `json:"explanation"`
	Source      string             `json:"source"`
	// first answers given during a first play of the lesson, and the wrong ones
	Asked  int `json:"asked"`
	Missed int `json:"missed"`
}

type adminGameLesson struct {
	ID         int64               `json:"id"`
	LevelID    int64               `json:"level_id"`
	LevelTitle gameText            `json:"level_title"`
	Course     string              `json:"course"`
	Position   int                 `json:"position"`
	Title      gameText            `json:"title"`
	Published  bool                `json:"published"`
	Players    int                 `json:"players"`
	Questions  []adminGameQuestion `json:"questions"`
}

// decodedText reads a text of the database; {} when it is empty.
func decodedText(raw json.RawMessage) gameText {
	text := gameText{}
	_ = json.Unmarshal(raw, &text)
	return text
}

// cleaned trims a text and drops its empty languages; it says what is wrong:
// a language the app does not have, no French when [required], or a text
// longer than [limit] characters.
func (text gameText) cleaned(name string, limit int, required bool) (gameText, error) {
	clean := gameText{}
	for lang, value := range text {
		if !gameLanguages[lang] {
			return nil, fmt.Errorf("%s: %q is not a language of the app", name, lang)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if utf8.RuneCountInString(value) > limit {
			return nil, fmt.Errorf("%s: %d characters at most", name, limit)
		}
		clean[lang] = value
	}
	if required && clean[gameDefaultLang] == "" {
		return nil, fmt.Errorf("%s: the French text is missing", name)
	}
	return clean, nil
}

func gameContentError(c *gin.Context, code string, err error) {
	c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, code, err))
}

func gamePlayedError(c *gin.Context) {
	gameContentError(c, "GAME_CONTENT_PLAYED", fmt.Errorf("members played it: make it a draft instead of deleting it"))
}

func lessonTooShortError(c *gin.Context) {
	gameContentError(c, "LESSON_TOO_SHORT", fmt.Errorf("a published lesson has at least %d questions", gameLessonQuestions))
}

// gameID reads the :id of a level, a lesson or a question.
func gameID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		badRequestError(c, fmt.Errorf("the id is a number"))
		return 0, false
	}
	return id, true
}

// ----- reading -----

// GET /v1/admin/games/courses: every course, drafts included.
func (s *Server) listAdminGameCourses(c *gin.Context) {
	rows, err := s.store.ListAdminGameCourses(c)
	if err != nil {
		internalServerError(c, err)
		return
	}
	courses := make([]adminGameCourseSummary, 0, len(rows))
	for _, row := range rows {
		courses = append(courses, adminGameCourseSummary{
			Code: row.Code, Position: int(row.Position), Title: decodedText(row.Title), Published: row.PublishedAt.Valid,
			Levels: int(row.Levels), Lessons: int(row.Lessons), Questions: int(row.Questions),
		})
	}
	apiResponse(c, http.StatusOK, courses)
}

// adminCourse reads a course with its levels and their lessons.
func (s *Server) adminCourse(c *gin.Context, code string) (adminGameCourse, error) {
	row, err := s.store.GetGameCourseByCode(c, code)
	if err != nil {
		return adminGameCourse{}, err
	}
	levels, err := s.store.ListAdminGameLevels(c, row.ID)
	if err != nil {
		return adminGameCourse{}, err
	}
	lessons, err := s.store.ListAdminGameLessons(c, row.ID)
	if err != nil {
		return adminGameCourse{}, err
	}
	course := adminGameCourse{
		Code: row.Code, Position: int(row.Position), Title: decodedText(row.Title), Published: row.PublishedAt.Valid,
		Levels: make([]adminGameLevel, 0, len(levels)),
	}
	at := map[int64]int{}
	for _, level := range levels {
		at[level.ID] = len(course.Levels)
		course.Levels = append(course.Levels, adminGameLevel{
			ID: level.ID, Position: int(level.Position), Title: decodedText(level.Title),
			Description: decodedText(level.Description), Color: level.Color, Published: level.PublishedAt.Valid,
			Lessons: []adminGameLessonSummary{},
		})
	}
	for _, lesson := range lessons {
		level := &course.Levels[at[lesson.LevelID]]
		level.Lessons = append(level.Lessons, adminGameLessonSummary{
			ID: lesson.ID, Position: int(lesson.Position), Title: decodedText(lesson.Title),
			Published: lesson.PublishedAt.Valid, Questions: int(lesson.Questions), Players: int(lesson.Players),
		})
	}
	return course, nil
}

// answerCourse answers with the course whole (after a write, or to read it).
func (s *Server) answerCourse(c *gin.Context, status int, code string) {
	course, err := s.adminCourse(c, code)
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "course")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, status, course)
}

// GET /v1/admin/games/courses/:code
func (s *Server) getAdminGameCourse(c *gin.Context) {
	s.answerCourse(c, http.StatusOK, c.Param("code"))
}

// answerLesson answers with the lesson and its questions.
func (s *Server) answerLesson(c *gin.Context, status int, id int64) {
	row, err := s.store.GetAdminGameLesson(c, id)
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "lesson")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	questions, err := s.store.ListAdminGameQuestions(c, id)
	if err != nil {
		internalServerError(c, err)
		return
	}
	lesson := adminGameLesson{
		ID: row.ID, LevelID: row.LevelID, LevelTitle: decodedText(row.LevelTitle), Course: row.CourseCode,
		Position: int(row.Position), Title: decodedText(row.Title), Published: row.PublishedAt.Valid,
		Players: int(row.Players), Questions: make([]adminGameQuestion, 0, len(questions)),
	}
	for _, question := range questions {
		var choices []importGameChoice
		_ = json.Unmarshal(question.Choices, &choices)
		lesson.Questions = append(lesson.Questions, adminGameQuestion{
			ID: question.ID, Position: int(question.Position), Kind: question.Kind, Prompt: decodedText(question.Prompt),
			Choices: choices, Answer: question.Answer, Explanation: decodedText(question.Explanation),
			Source: question.Source, Asked: int(question.Asked), Missed: int(question.Missed),
		})
	}
	apiResponse(c, status, lesson)
}

// GET /v1/admin/games/lessons/:id
func (s *Server) getAdminGameLesson(c *gin.Context) {
	if id, ok := gameID(c); ok {
		s.answerLesson(c, http.StatusOK, id)
	}
}

// ----- the courses -----

type gameCourseRequest struct {
	Code      string   `json:"code"`
	Title     gameText `json:"title"`
	Published bool     `json:"published"`
}

// POST /v1/admin/games/courses: a new course, a draft.
func (s *Server) createGameCourse(c *gin.Context) {
	var request gameCourseRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequestError(c, err)
		return
	}
	if !gameCourseCode.MatchString(request.Code) {
		badRequestError(c, fmt.Errorf("code: lowercase letters, digits, - and _ (40 at most), starting with a letter"))
		return
	}
	title, err := request.Title.cleaned("title", gameTitleLength, true)
	if err != nil {
		badRequestError(c, err)
		return
	}
	if _, err := s.store.GetGameCourseByCode(c, request.Code); err == nil {
		conflictError(c, fmt.Errorf("a course already has the code %q", request.Code))
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		internalServerError(c, err)
		return
	}
	if err := s.store.CreateGameCourse(c, db.CreateGameCourseParams{Code: request.Code, Title: title.encoded()}); err != nil {
		internalServerError(c, err)
		return
	}
	s.answerCourse(c, http.StatusCreated, request.Code)
}

// PUT /v1/admin/games/courses/:code: its title, published or not.
func (s *Server) updateGameCourse(c *gin.Context) {
	var request gameCourseRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequestError(c, err)
		return
	}
	title, err := request.Title.cleaned("title", gameTitleLength, true)
	if err != nil {
		badRequestError(c, err)
		return
	}
	course, err := s.store.GetGameCourseByCode(c, c.Param("code"))
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "course")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	if request.Published {
		levels, err := s.store.CountPublishedGameLevels(c, course.ID)
		if err != nil {
			internalServerError(c, err)
			return
		}
		if levels == 0 {
			gameContentError(c, "COURSE_EMPTY", fmt.Errorf("a published course has at least one published level"))
			return
		}
	}
	if err := s.store.UpdateGameCourse(c, db.UpdateGameCourseParams{
		ID: course.ID, Title: title.encoded(), Published: request.Published, Now: gameClock(),
	}); err != nil {
		internalServerError(c, err)
		return
	}
	s.answerCourse(c, http.StatusOK, course.Code)
}

// ----- the levels -----

type gameLevelRequest struct {
	Title       gameText `json:"title"`
	Description gameText `json:"description"`
	Color       string   `json:"color"`
	Published   bool     `json:"published"`
}

// read checks a level; false once it answered 400.
func (r *gameLevelRequest) read(c *gin.Context) bool {
	if err := c.ShouldBindJSON(r); err != nil {
		badRequestError(c, err)
		return false
	}
	var err error
	if r.Title, err = r.Title.cleaned("title", gameTitleLength, true); err != nil {
		badRequestError(c, err)
		return false
	}
	if r.Description, err = r.Description.cleaned("description", gameDescription, false); err != nil {
		badRequestError(c, err)
		return false
	}
	if r.Color == "" {
		r.Color = gameDefaultColor
	}
	if !gameColor.MatchString(r.Color) {
		badRequestError(c, fmt.Errorf("color: written #RRGGBB"))
		return false
	}
	return true
}

// POST /v1/admin/games/courses/:code/levels: a new level, a draft, last.
func (s *Server) createGameLevel(c *gin.Context) {
	var request gameLevelRequest
	if !request.read(c) {
		return
	}
	course, err := s.store.GetGameCourseByCode(c, c.Param("code"))
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "course")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	if err := s.store.AppendGameLevel(c, db.AppendGameLevelParams{
		CourseID: course.ID, Title: request.Title.encoded(), Description: request.Description.encoded(), Color: request.Color,
	}); err != nil {
		internalServerError(c, err)
		return
	}
	s.answerCourse(c, http.StatusCreated, course.Code)
}

// adminLevel reads the level of :id; false once it answered.
func (s *Server) adminLevel(c *gin.Context) (db.GetAdminGameLevelRow, bool) {
	id, ok := gameID(c)
	if !ok {
		return db.GetAdminGameLevelRow{}, false
	}
	level, err := s.store.GetAdminGameLevel(c, id)
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "level")
		return level, false
	}
	if err != nil {
		internalServerError(c, err)
		return level, false
	}
	return level, true
}

// PUT /v1/admin/games/levels/:id
func (s *Server) updateGameLevel(c *gin.Context) {
	var request gameLevelRequest
	if !request.read(c) {
		return
	}
	level, ok := s.adminLevel(c)
	if !ok {
		return
	}
	if request.Published {
		lessons, err := s.store.CountPublishedGameLessons(c, level.ID)
		if err != nil {
			internalServerError(c, err)
			return
		}
		if lessons == 0 {
			gameContentError(c, "LEVEL_EMPTY", fmt.Errorf("a published level has at least one published lesson"))
			return
		}
	}
	if err := s.store.UpdateGameLevel(c, db.UpdateGameLevelParams{
		ID: level.ID, Title: request.Title.encoded(), Description: request.Description.encoded(),
		Color: request.Color, Published: request.Published, Now: gameClock(),
	}); err != nil {
		internalServerError(c, err)
		return
	}
	s.answerCourse(c, http.StatusOK, level.CourseCode)
}

// DELETE /v1/admin/games/levels/:id: with its lessons, unless members played
// one of them.
func (s *Server) deleteGameLevel(c *gin.Context) {
	level, ok := s.adminLevel(c)
	if !ok {
		return
	}
	played, err := s.store.CountGameResultsOfLevel(c, level.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	if played > 0 {
		gamePlayedError(c)
		return
	}
	if err := s.store.DeleteGameLevel(c, level.ID); err != nil {
		internalServerError(c, err)
		return
	}
	s.answerCourse(c, http.StatusOK, level.CourseCode)
}

// ----- the lessons -----

type gameLessonRequest struct {
	Title     gameText `json:"title"`
	Published bool     `json:"published"`
}

func (r *gameLessonRequest) read(c *gin.Context) bool {
	if err := c.ShouldBindJSON(r); err != nil {
		badRequestError(c, err)
		return false
	}
	var err error
	if r.Title, err = r.Title.cleaned("title", gameTitleLength, true); err != nil {
		badRequestError(c, err)
		return false
	}
	return true
}

// POST /v1/admin/games/levels/:id/lessons: a new lesson, a draft, last.
func (s *Server) createGameLesson(c *gin.Context) {
	var request gameLessonRequest
	if !request.read(c) {
		return
	}
	level, ok := s.adminLevel(c)
	if !ok {
		return
	}
	if err := s.store.AppendGameLesson(c, db.AppendGameLessonParams{LevelID: level.ID, Title: request.Title.encoded()}); err != nil {
		internalServerError(c, err)
		return
	}
	s.answerCourse(c, http.StatusCreated, level.CourseCode)
}

// adminLesson reads the lesson of [id]; false once it answered.
func (s *Server) adminLesson(c *gin.Context, id int64) (db.GetAdminGameLessonRow, bool) {
	lesson, err := s.store.GetAdminGameLesson(c, id)
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "lesson")
		return lesson, false
	}
	if err != nil {
		internalServerError(c, err)
		return lesson, false
	}
	return lesson, true
}

// PUT /v1/admin/games/lessons/:id: its title, published or not.
func (s *Server) updateGameLesson(c *gin.Context) {
	var request gameLessonRequest
	if !request.read(c) {
		return
	}
	id, ok := gameID(c)
	if !ok {
		return
	}
	lesson, ok := s.adminLesson(c, id)
	if !ok {
		return
	}
	if request.Published {
		questions, err := s.store.CountActiveGameQuestions(c, lesson.ID)
		if err != nil {
			internalServerError(c, err)
			return
		}
		if questions < gameLessonQuestions {
			lessonTooShortError(c)
			return
		}
	}
	if err := s.store.UpdateGameLesson(c, db.UpdateGameLessonParams{
		ID: lesson.ID, Title: request.Title.encoded(), Published: request.Published, Now: gameClock(),
	}); err != nil {
		internalServerError(c, err)
		return
	}
	s.answerLesson(c, http.StatusOK, lesson.ID)
}

// DELETE /v1/admin/games/lessons/:id: unless members played it.
func (s *Server) deleteGameLesson(c *gin.Context) {
	id, ok := gameID(c)
	if !ok {
		return
	}
	lesson, ok := s.adminLesson(c, id)
	if !ok {
		return
	}
	if lesson.Players > 0 {
		gamePlayedError(c)
		return
	}
	if err := s.store.DeleteGameLesson(c, lesson.ID); err != nil {
		internalServerError(c, err)
		return
	}
	s.answerCourse(c, http.StatusOK, lesson.CourseCode)
}

// ----- the questions -----

// readGameQuestion checks a question like the import does, its texts
// cleaned; false once it answered 400.
func readGameQuestion(c *gin.Context) (importGameQuestion, bool) {
	var question importGameQuestion
	if err := c.ShouldBindJSON(&question); err != nil {
		badRequestError(c, err)
		return question, false
	}
	fail := func(err error) (importGameQuestion, bool) {
		badRequestError(c, err)
		return question, false
	}
	var err error
	if question.Prompt, err = question.Prompt.cleaned("prompt", gamePromptLength, true); err != nil {
		return fail(err)
	}
	if question.Explanation, err = question.Explanation.cleaned("explanation", gameExplanationLimit, false); err != nil {
		return fail(err)
	}
	if len(question.Choices) > gameMaxChoices {
		return fail(fmt.Errorf("choices: %d at most", gameMaxChoices))
	}
	for i, choice := range question.Choices {
		if question.Choices[i].Text, err = choice.Text.cleaned("choice", gameChoiceLength, false); err != nil {
			return fail(err)
		}
	}
	question.Source = strings.TrimSpace(question.Source)
	if err := question.validate(); err != nil {
		return fail(err)
	}
	return question, true
}

// POST /v1/admin/games/lessons/:id/questions: a new question, last.
func (s *Server) createGameQuestion(c *gin.Context) {
	question, ok := readGameQuestion(c)
	if !ok {
		return
	}
	id, ok := gameID(c)
	if !ok {
		return
	}
	lesson, ok := s.adminLesson(c, id)
	if !ok {
		return
	}
	choices, _ := json.Marshal(question.Choices)
	if err := s.store.AppendGameQuestion(c, db.AppendGameQuestionParams{
		LessonID: lesson.ID, Kind: question.Kind, Prompt: question.Prompt.encoded(), Choices: choices,
		Answer: question.Answer, Explanation: question.Explanation.encoded(), Source: question.Source,
	}); err != nil {
		internalServerError(c, err)
		return
	}
	s.answerLesson(c, http.StatusCreated, lesson.ID)
}

// adminQuestion reads the question of :id; false once it answered.
func (s *Server) adminQuestion(c *gin.Context) (db.GetAdminGameQuestionRow, bool) {
	id, ok := gameID(c)
	if !ok {
		return db.GetAdminGameQuestionRow{}, false
	}
	question, err := s.store.GetAdminGameQuestion(c, id)
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "question")
		return question, false
	}
	if err != nil {
		internalServerError(c, err)
		return question, false
	}
	return question, true
}

// PUT /v1/admin/games/questions/:id: corrected, it keeps its id and what the
// members missed of it.
func (s *Server) updateGameQuestion(c *gin.Context) {
	question, ok := readGameQuestion(c)
	if !ok {
		return
	}
	current, ok := s.adminQuestion(c)
	if !ok {
		return
	}
	choices, _ := json.Marshal(question.Choices)
	if err := s.store.UpdateGameQuestion(c, db.UpdateGameQuestionParams{
		ID: current.ID, Kind: question.Kind, Prompt: question.Prompt.encoded(), Choices: choices,
		Answer: question.Answer, Explanation: question.Explanation.encoded(), Source: question.Source,
	}); err != nil {
		internalServerError(c, err)
		return
	}
	s.answerLesson(c, http.StatusOK, current.LessonID)
}

// DELETE /v1/admin/games/questions/:id: a published lesson keeps 8 questions.
func (s *Server) deleteGameQuestion(c *gin.Context) {
	current, ok := s.adminQuestion(c)
	if !ok {
		return
	}
	if current.LessonPublished {
		questions, err := s.store.CountActiveGameQuestions(c, current.LessonID)
		if err != nil {
			internalServerError(c, err)
			return
		}
		if questions <= gameLessonQuestions {
			lessonTooShortError(c)
			return
		}
	}
	if err := s.store.DeleteGameQuestion(c, current.ID); err != nil {
		internalServerError(c, err)
		return
	}
	s.answerLesson(c, http.StatusOK, current.LessonID)
}

// ----- the order -----

type gameOrderRequest struct {
	IDs []int64 `json:"ids"`
}

// ordered runs a reordering and says whether it went; it answered if not.
func ordered(c *gin.Context, reorder func(ids []int64) error) bool {
	var request gameOrderRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequestError(c, err)
		return false
	}
	err := reorder(request.IDs)
	if errors.Is(err, db.ErrGameOrderOutOfDate) {
		gameContentError(c, "ORDER_OUT_OF_DATE", err)
		return false
	}
	if err != nil {
		internalServerError(c, err)
		return false
	}
	return true
}

// PUT /v1/admin/games/courses/:code/levels/order: {ids} in the new order.
func (s *Server) orderGameLevels(c *gin.Context) {
	course, err := s.store.GetGameCourseByCode(c, c.Param("code"))
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "course")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	if ordered(c, func(ids []int64) error { return s.store.ReorderGameLevelsTx(c, course.ID, ids) }) {
		s.answerCourse(c, http.StatusOK, course.Code)
	}
}

// PUT /v1/admin/games/levels/:id/lessons/order
func (s *Server) orderGameLessons(c *gin.Context) {
	level, ok := s.adminLevel(c)
	if !ok {
		return
	}
	if ordered(c, func(ids []int64) error { return s.store.ReorderGameLessonsTx(c, level.ID, ids) }) {
		s.answerCourse(c, http.StatusOK, level.CourseCode)
	}
}

// PUT /v1/admin/games/lessons/:id/questions/order
func (s *Server) orderGameQuestions(c *gin.Context) {
	id, ok := gameID(c)
	if !ok {
		return
	}
	lesson, ok := s.adminLesson(c, id)
	if !ok {
		return
	}
	if ordered(c, func(ids []int64) error { return s.store.ReorderGameQuestionsTx(c, lesson.ID, ids) }) {
		s.answerLesson(c, http.StatusOK, lesson.ID)
	}
}
