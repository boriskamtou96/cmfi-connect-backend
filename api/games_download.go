package api

import (
	"net/http"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

type gameCourseDownloadResponse struct {
	Course  string               `json:"course"`
	Lessons []gameLessonResponse `json:"lessons"`
}

// GET /v1/games/courses/:code/download?lang=: every published lesson of a
// course with its questions, for the phone to keep and play without
// network. The lessons still open one after the other on the phone; the
// plays sent later in the order they were played open them here in turn.
func (s *Server) downloadGameCourse(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	lang := gameLang(c)
	code := c.Param("code")

	courses, err := s.store.ListGameCourses(c)
	if err != nil {
		internalServerError(c, err)
		return
	}
	var courseID int64
	for _, course := range courses {
		if course.Code == code {
			courseID = course.ID
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
	levelTitles := make(map[int64]string, len(levels))
	for _, level := range levels {
		levelTitles[level.ID] = textIn(level.Title, lang)
	}
	lessons, err := s.store.ListGameLessonsOfCourse(c, db.ListGameLessonsOfCourseParams{UserID: authUser.ID, CourseID: courseID})
	if err != nil {
		internalServerError(c, err)
		return
	}

	response := gameCourseDownloadResponse{Course: code, Lessons: make([]gameLessonResponse, 0, len(lessons))}
	for _, lesson := range lessons {
		rows, err := s.store.ListGameQuestionsOfLesson(c, lesson.ID)
		if err != nil {
			internalServerError(c, err)
			return
		}
		response.Lessons = append(response.Lessons, gameLessonResponse{
			ID: lesson.ID, Title: textIn(lesson.Title, lang), LevelTitle: levelTitles[lesson.LevelID],
			Course: code, Questions: toGameQuestions(rows, lang),
		})
	}
	apiResponse(c, http.StatusOK, response)
}
