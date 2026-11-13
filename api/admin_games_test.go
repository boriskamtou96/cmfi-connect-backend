package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/stretchr/testify/require"
)

// sampleCourse is a published course of one level: [lessons] lessons of 8
// questions whose right choice is always "a".
func sampleCourse(code string, lessons int) map[string]any {
	var list []map[string]any
	for l := 1; l <= lessons; l++ {
		var questions []map[string]any
		for q := 1; q <= 8; q++ {
			questions = append(questions, map[string]any{
				"kind":   "choice",
				"prompt": map[string]string{"fr": fmt.Sprintf("Question %d de la leçon %d ?", q, l)},
				"choices": []map[string]any{
					{"id": "a", "text": map[string]string{"fr": "La bonne", "en": "The right one"}},
					{"id": "b", "text": map[string]string{"fr": "Une autre"}},
					{"id": "c", "text": map[string]string{"fr": "Encore une autre"}},
				},
				"answer":      "a",
				"explanation": map[string]string{"fr": "Parce que."},
				"source":      "ministry:founder",
			})
		}
		list = append(list, map[string]any{
			"title":     map[string]string{"fr": fmt.Sprintf("Leçon %d", l)},
			"questions": questions,
		})
	}
	return map[string]any{
		"code":      code,
		"position":  1,
		"title":     map[string]string{"fr": "Notre ministère", "en": "Our ministry"},
		"published": true,
		"levels": []map[string]any{{
			"title":       map[string]string{"fr": "Notre fondateur"},
			"description": map[string]string{"fr": "Sa vie"},
			"color":       "#0B3B70",
			"lessons":     list,
		}},
	}
}

func newCourseCode() string { return fmt.Sprintf("test-%d", rand.IntN(1_000_000_000)) }

// adminToken is the token of a fresh admin.
func adminToken(t *testing.T, router http.Handler) string {
	admin := registerMember(t, router, nil)
	setRole(t, admin.ID, roleAdmin)
	return admin.Token
}

// importSampleCourse imports [sampleCourse] under a new code (gone with the
// test) and gives the code and the ids of its lessons, in order.
func importSampleCourse(t *testing.T, router http.Handler, lessons int) (string, []int64) {
	code := newCourseCode()
	response := send(t, router, http.MethodPost, adminToken(t, router), "/v1/admin/games/import", sampleCourse(code, lessons))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	t.Cleanup(func() { _ = testStore.DeleteGameCourse(context.Background(), code) })

	rows, err := testStore.ListGameLessonsOfCourse(context.Background(), courseLessonsParams(t, code))
	require.NoError(t, err)
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	require.Len(t, ids, lessons)
	return code, ids
}

func TestACourseIsImportedByAnAdminOnly(t *testing.T) {
	router := newTestRouter(t)
	code := newCourseCode()
	t.Cleanup(func() { _ = testStore.DeleteGameCourse(context.Background(), code) })

	member := registerMember(t, router, nil)
	refused := send(t, router, http.MethodPost, member.Token, "/v1/admin/games/import", sampleCourse(code, 2))
	require.Equal(t, http.StatusForbidden, refused.Code)

	imported := send(t, router, http.MethodPost, adminToken(t, router), "/v1/admin/games/import", sampleCourse(code, 2))
	require.Equal(t, http.StatusOK, imported.Code, imported.Body.String())
	require.Equal(t, gameImportResponse{Code: code, Levels: 1, Lessons: 2, Questions: 16}, decode[gameImportResponse](t, imported))
}

func TestAnImportIsRefusedWithoutFrenchOrWithAWrongAnswer(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)

	firstQuestion := func(course map[string]any) map[string]any {
		level := course["levels"].([]map[string]any)[0]
		return level["lessons"].([]map[string]any)[0]["questions"].([]map[string]any)[0]
	}
	for name, spoil := range map[string]func(course map[string]any){
		"a title without French":          func(c map[string]any) { c["title"] = map[string]string{"en": "Our ministry"} },
		"a code with spaces":              func(c map[string]any) { c["code"] = "Notre ministère" },
		"no level":                        func(c map[string]any) { c["levels"] = []map[string]any{} },
		"an answer not among the choices": func(c map[string]any) { firstQuestion(c)["answer"] = "z" },
		"a single choice": func(c map[string]any) {
			firstQuestion(c)["choices"] = []map[string]any{{"id": "a", "text": map[string]string{"fr": "Seul"}}}
		},
		"an unknown kind":         func(c map[string]any) { firstQuestion(c)["kind"] = "riddle" },
		"a prompt without French": func(c map[string]any) { firstQuestion(c)["prompt"] = map[string]string{"en": "Who?"} },
		"a color that is not one": func(c map[string]any) { c["levels"].([]map[string]any)[0]["color"] = "blue" },
		"two choices with one id": func(c map[string]any) {
			firstQuestion(c)["choices"] = []map[string]any{
				{"id": "a", "text": map[string]string{"fr": "Un"}}, {"id": "a", "text": map[string]string{"fr": "Deux"}},
			}
		},
	} {
		code := newCourseCode()
		course := sampleCourse(code, 1)
		spoil(course)
		response := send(t, router, http.MethodPost, token, "/v1/admin/games/import", course)
		require.Equal(t, http.StatusBadRequest, response.Code, "%s: %s", name, response.Body.String())
		_, err := testStore.GetGameCourseByCode(context.Background(), code)
		require.Error(t, err, "%s: nothing is created", name)
	}
}

func TestAnImportReplacesTheCourseUntilSomeonePlayed(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	code, lessons := importSampleCourse(t, router, 3)

	// imported again with two lessons: the course is replaced
	again := send(t, router, http.MethodPost, token, "/v1/admin/games/import", sampleCourse(code, 2))
	require.Equal(t, http.StatusOK, again.Code, again.Body.String())
	rows, err := testStore.ListGameLessonsOfCourse(context.Background(), courseLessonsParams(t, code))
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.NotContains(t, []int64{rows[0].ID, rows[1].ID}, lessons[0])

	// a member finished a lesson: importing would erase it
	member := registerMember(t, router, nil)
	markLessonDone(t, member.ID, rows[0].ID)
	refused := send(t, router, http.MethodPost, token, "/v1/admin/games/import", sampleCourse(code, 4))
	require.Equal(t, http.StatusConflict, refused.Code, refused.Body.String())
	rows, err = testStore.ListGameLessonsOfCourse(context.Background(), courseLessonsParams(t, code))
	require.NoError(t, err)
	require.Len(t, rows, 2)
}

// courseLessonsParams asks for the lessons of course [code], as seen by nobody.
func courseLessonsParams(t *testing.T, code string) db.ListGameLessonsOfCourseParams {
	course, err := testStore.GetGameCourseByCode(context.Background(), code)
	require.NoError(t, err)
	return db.ListGameLessonsOfCourseParams{CourseID: course.ID}
}

// markLessonDone writes that [userID] finished the lesson, without playing it.
func markLessonDone(t *testing.T, userID, lessonID int64) {
	_, err := testStore.UpsertGameLessonResult(context.Background(), db.UpsertGameLessonResultParams{
		UserID: userID, LessonID: lessonID, Stars: 3, FirstTry: 8, PlayedAt: time.Now(),
	})
	require.NoError(t, err)
}

func TestTheShippedCoursesAreValid(t *testing.T) {
	files, err := filepath.Glob("../data/games/*.json")
	require.NoError(t, err)
	require.NotEmpty(t, files, "no course in data/games")
	for _, file := range files {
		raw, err := os.ReadFile(file)
		require.NoError(t, err)
		var course importGameCourseRequest
		require.NoError(t, json.Unmarshal(raw, &course), file)
		require.NoError(t, course.validate(), file)
		for l, level := range course.Levels {
			for n, lesson := range level.Lessons {
				at := fmt.Sprintf("%s: level %d, lesson %d", file, l+1, n+1)
				require.Len(t, lesson.Questions, 8, at)
				prompts := map[string]bool{}
				for _, question := range lesson.Questions {
					require.NotEmpty(t, question.Source, "%s: %s", at, question.Prompt["fr"])
					require.NotEmpty(t, question.Explanation["fr"], "%s: %s", at, question.Prompt["fr"])
					require.False(t, prompts[question.Prompt["fr"]], "%s: twice %q", at, question.Prompt["fr"])
					prompts[question.Prompt["fr"]] = true
				}
			}
		}
	}
}
