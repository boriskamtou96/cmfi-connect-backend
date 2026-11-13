package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
)

// The content of the games comes in by an import: one JSON file per course,
// its levels, lessons and questions in order. Editing one question at a time
// comes with the back-office.

// gameText is a text in the languages of the app: {"fr": "…", "en": "…"}.
// French is always there; the others fall back on it.
type gameText map[string]string

const gameDefaultLang = "fr"

var (
	gameCourseCode = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
	gameColor      = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	gameKinds      = map[string]bool{"choice": true, "true_false": true, "fill_verse": true}
)

const gameDefaultColor = "#0B3B70"

type importGameCourseRequest struct {
	Code      string            `json:"code"`
	Position  int32             `json:"position"`
	Title     gameText          `json:"title"`
	Published bool              `json:"published"`
	Levels    []importGameLevel `json:"levels"`
}

type importGameLevel struct {
	Title       gameText           `json:"title"`
	Description gameText           `json:"description"`
	Color       string             `json:"color"`
	Lessons     []importGameLesson `json:"lessons"`
}

type importGameLesson struct {
	Title     gameText             `json:"title"`
	Questions []importGameQuestion `json:"questions"`
}

type importGameQuestion struct {
	Kind        string             `json:"kind"`
	Prompt      gameText           `json:"prompt"`
	Choices     []importGameChoice `json:"choices"`
	Answer      string             `json:"answer"`
	Explanation gameText           `json:"explanation"`
	Source      string             `json:"source"`
}

type importGameChoice struct {
	ID   string   `json:"id"`
	Text gameText `json:"text"`
}

type gameImportResponse struct {
	Code      string `json:"code"`
	Levels    int    `json:"levels"`
	Lessons   int    `json:"lessons"`
	Questions int    `json:"questions"`
}

func (text gameText) hasFrench() bool { return strings.TrimSpace(text[gameDefaultLang]) != "" }

// validate says what is wrong with a course to import, naming the place.
func (r importGameCourseRequest) validate() error {
	if !gameCourseCode.MatchString(r.Code) {
		return fmt.Errorf("code: lowercase letters, digits, - and _ (40 at most), starting with a letter")
	}
	if !r.Title.hasFrench() {
		return fmt.Errorf("title: the French text is missing")
	}
	if len(r.Levels) == 0 {
		return fmt.Errorf("levels: a course has at least one level")
	}
	for l, level := range r.Levels {
		at := fmt.Sprintf("level %d", l+1)
		if !level.Title.hasFrench() {
			return fmt.Errorf("%s: the French title is missing", at)
		}
		if level.Color != "" && !gameColor.MatchString(level.Color) {
			return fmt.Errorf("%s: the color is written #RRGGBB", at)
		}
		if len(level.Lessons) == 0 {
			return fmt.Errorf("%s: a level has at least one lesson", at)
		}
		for n, lesson := range level.Lessons {
			at := fmt.Sprintf("%s, lesson %d", at, n+1)
			if !lesson.Title.hasFrench() {
				return fmt.Errorf("%s: the French title is missing", at)
			}
			if len(lesson.Questions) == 0 {
				return fmt.Errorf("%s: a lesson has at least one question", at)
			}
			for q, question := range lesson.Questions {
				if err := question.validate(); err != nil {
					return fmt.Errorf("%s, question %d: %w", at, q+1, err)
				}
			}
		}
	}
	return nil
}

func (q importGameQuestion) validate() error {
	if !gameKinds[q.Kind] {
		return fmt.Errorf("unknown kind %q", q.Kind)
	}
	if !q.Prompt.hasFrench() {
		return fmt.Errorf("the French prompt is missing")
	}
	if len(q.Choices) < 2 {
		return fmt.Errorf("a question has at least two choices")
	}
	if q.Kind == "true_false" && len(q.Choices) != 2 {
		return fmt.Errorf("true or false has two choices")
	}
	if len(q.Source) > 120 {
		return fmt.Errorf("the source is 120 characters at most")
	}
	ids := map[string]bool{}
	for _, choice := range q.Choices {
		id := strings.TrimSpace(choice.ID)
		if id == "" || len(id) > 20 || id != choice.ID {
			return fmt.Errorf("a choice needs a short id")
		}
		if ids[id] {
			return fmt.Errorf("two choices have the id %q", id)
		}
		ids[id] = true
		if !choice.Text.hasFrench() {
			return fmt.Errorf("the French text of choice %q is missing", id)
		}
	}
	if !ids[q.Answer] {
		return fmt.Errorf("the answer %q is not one of the choices", q.Answer)
	}
	return nil
}

// encoded is the text as the database keeps it; {} when there is none.
func (text gameText) encoded() json.RawMessage {
	if text == nil {
		return json.RawMessage(`{}`)
	}
	raw, _ := json.Marshal(text)
	return raw
}

func (r importGameCourseRequest) toImport() db.GameCourseImport {
	course := db.GameCourseImport{
		Code: r.Code, Position: max(r.Position, 1), Title: r.Title.encoded(), Published: r.Published,
	}
	for _, level := range r.Levels {
		color := level.Color
		if color == "" {
			color = gameDefaultColor
		}
		out := db.GameLevelImport{Title: level.Title.encoded(), Description: level.Description.encoded(), Color: color}
		for _, lesson := range level.Lessons {
			item := db.GameLessonImport{Title: lesson.Title.encoded()}
			for _, question := range lesson.Questions {
				choices, _ := json.Marshal(question.Choices)
				item.Questions = append(item.Questions, db.CreateGameQuestionParams{
					Kind: question.Kind, Prompt: question.Prompt.encoded(), Choices: choices,
					Answer: question.Answer, Explanation: question.Explanation.encoded(), Source: question.Source,
				})
			}
			out.Lessons = append(out.Lessons, item)
		}
		course.Levels = append(course.Levels, out)
	}
	return course
}

// POST /v1/admin/games/import: a course with its levels, lessons and
// questions; it replaces the course of the same code. Refused once members
// played it: their progress would go.
func (s *Server) importGameCourse(c *gin.Context) {
	var request importGameCourseRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequestError(c, err)
		return
	}
	if err := request.validate(); err != nil {
		badRequestError(c, err)
		return
	}

	result, err := s.store.ImportGameCourseTx(c, request.toImport(), time.Now())
	if errors.Is(err, db.ErrGameCoursePlayed) {
		conflictError(c, fmt.Errorf("members already played this course: importing it again would erase their progress"))
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, gameImportResponse{
		Code: request.Code, Levels: result.Levels, Lessons: result.Lessons, Questions: result.Questions,
	})
}
