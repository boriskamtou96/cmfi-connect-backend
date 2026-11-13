package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func gameItem(id, fr string) map[string]any {
	return map[string]any{"id": id, "text": map[string]string{"fr": fr}}
}

// orderQuestion: the days of creation, to put in order (the right one is
// c, a, d, b).
func orderQuestion() map[string]any {
	return map[string]any{
		"kind":   "order",
		"prompt": map[string]string{"fr": "Remets dans l'ordre"},
		"choices": []map[string]any{
			gameItem("a", "La lumière"), gameItem("b", "L'homme"), gameItem("c", "Au commencement"), gameItem("d", "Les astres"),
		},
		"answer": "c,a,d,b",
	}
}

func matchQuestion() map[string]any {
	return map[string]any{
		"kind":   "match",
		"prompt": map[string]string{"fr": "Relie chacun à son fait"},
		"choices": []map[string]any{
			gameItem("l1", "Noé"), gameItem("l2", "Moïse"), gameItem("l3", "David"),
			gameItem("r1", "Goliath"), gameItem("r2", "L'arche"), gameItem("r3", "La mer Rouge"),
		},
		"answer": "l1=r2,l2=r3,l3=r1",
	}
}

// withQuestion is a course of one lesson whose first question is [question].
func withQuestion(code string, question map[string]any) map[string]any {
	course := sampleCourse(code, 1)
	lesson := course["levels"].([]map[string]any)[0]["lessons"].([]map[string]any)[0]
	lesson["questions"].([]map[string]any)[0] = question
	return course
}

func TestOrderAndMatchQuestionsAreImportedAndPlayed(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	code := newCourseCode()
	t.Cleanup(func() { _ = testStore.DeleteGameCourse(context.Background(), code) })

	course := withQuestion(code, orderQuestion())
	lesson := course["levels"].([]map[string]any)[0]["lessons"].([]map[string]any)[0]
	lesson["questions"].([]map[string]any)[1] = matchQuestion()
	response := send(t, router, http.MethodPost, token, "/v1/admin/games/import", course)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	m := registerMember(t, router, nil)
	rows, err := testStore.ListGameLessonsOfCourse(context.Background(), courseLessonsParams(t, code))
	require.NoError(t, err)
	lessonID := rows[0].ID
	got := decode[gameLessonResponse](t, send(t, router, http.MethodGet, m.Token, fmt.Sprintf("/v1/games/lessons/%d", lessonID), nil))
	require.Equal(t, "order", got.Questions[0].Kind)
	require.Equal(t, "c,a,d,b", got.Questions[0].Answer)
	require.Equal(t, "match", got.Questions[1].Kind)
	require.Equal(t, "l1=r2,l2=r3,l3=r1", got.Questions[1].Answer)
	require.Len(t, got.Questions[1].Choices, 6)

	// all right at once, the order and the pairs included: 8 out of 8
	result := playLesson(t, router, m.Token, lessonID, "order-match-1", "JJJJJJJJ")
	require.Equal(t, 8, result.FirstTry)
	require.Equal(t, 3, result.Stars)
}

func TestAWrongOrderOrMatchIsRefusedOnImport(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	for name, question := range map[string]func() map[string]any{
		"an order with three things": func() map[string]any {
			q := orderQuestion()
			q["choices"] = q["choices"].([]map[string]any)[:3]
			q["answer"] = "c,a,b"
			return q
		},
		"an order that forgets one": func() map[string]any { q := orderQuestion(); q["answer"] = "c,a,d"; return q },
		"an order that repeats one": func() map[string]any { q := orderQuestion(); q["answer"] = "c,a,a,b"; return q },
		"a match with an odd id": func() map[string]any {
			q := matchQuestion()
			q["choices"].([]map[string]any)[0]["id"] = "x1"
			return q
		},
		"a match with a left alone": func() map[string]any {
			q := matchQuestion()
			q["choices"] = append(q["choices"].([]map[string]any), gameItem("l4", "Jonas"))
			return q
		},
		"a match not by left id": func() map[string]any { q := matchQuestion(); q["answer"] = "l2=r3,l1=r2,l3=r1"; return q },
		"a match that uses a right twice": func() map[string]any {
			q := matchQuestion()
			q["answer"] = "l1=r2,l2=r2,l3=r1"
			return q
		},
	} {
		code := newCourseCode()
		response := send(t, router, http.MethodPost, token, "/v1/admin/games/import", withQuestion(code, question()))
		require.Equal(t, http.StatusBadRequest, response.Code, "%s: %s", name, response.Body.String())
		_, err := testStore.GetGameCourseByCode(context.Background(), code)
		require.Error(t, err, "%s: nothing is created", name)
	}
}

func TestACourseIsDownloadedWithAllItsLessons(t *testing.T) {
	router := newTestRouter(t)
	code, lessons := importSampleCourse(t, router, 3)
	m := registerMember(t, router, nil)

	response := send(t, router, http.MethodGet, m.Token, "/v1/games/courses/"+code+"/download?lang=en", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	download := decode[gameCourseDownloadResponse](t, response)
	require.Equal(t, code, download.Course)
	// the locked ones too: the phone opens them one after the other
	require.Len(t, download.Lessons, 3)
	for i, lesson := range download.Lessons {
		require.Equal(t, lessons[i], lesson.ID)
		require.Equal(t, "Notre fondateur", lesson.LevelTitle)
		require.Equal(t, code, lesson.Course)
		require.Len(t, lesson.Questions, 8)
	}
	require.Equal(t, "The right one", download.Lessons[0].Questions[0].Choices[0].Text)

	// played later in the order they were played, they open here in turn
	playLesson(t, router, m.Token, lessons[0], "dl-1", "JJJJJJJJ")
	playLesson(t, router, m.Token, lessons[1], "dl-2", "JJJJJJJJ")
	require.Equal(t, []string{"done", "done", "open"}, pathStates(getPath(t, router, m.Token, code)))

	missing := send(t, router, http.MethodGet, m.Token, "/v1/games/courses/nothing-here/download", nil)
	require.Equal(t, http.StatusNotFound, missing.Code)
	anonymous := send(t, router, http.MethodGet, "", "/v1/games/courses/"+code+"/download", nil)
	require.Equal(t, http.StatusUnauthorized, anonymous.Code)
}
