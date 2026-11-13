package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// sampleQuestion is a question as the back-office sends it.
func sampleQuestion(prompt string) map[string]any {
	return map[string]any{
		"kind":   "choice",
		"prompt": map[string]string{"fr": prompt},
		"choices": []map[string]any{
			{"id": "a", "text": map[string]string{"fr": "La bonne"}},
			{"id": "b", "text": map[string]string{"fr": "Une autre"}},
		},
		"answer":      "a",
		"explanation": map[string]string{"fr": "Parce que."},
		"source":      "ministry:purpose",
	}
}

func adminGames(t *testing.T, router http.Handler, method, token, path string, body any) *httptest.ResponseRecorder {
	return send(t, router, method, token, "/v1/admin/games"+path, body)
}

// readCourse reads a course as the back-office sees it.
func readCourse(t *testing.T, router http.Handler, token, code string) adminGameCourse {
	response := adminGames(t, router, http.MethodGet, token, "/courses/"+code, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[adminGameCourse](t, response)
}

func readLesson(t *testing.T, router http.Handler, token string, id int64) adminGameLesson {
	response := adminGames(t, router, http.MethodGet, token, fmt.Sprintf("/lessons/%d", id), nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[adminGameLesson](t, response)
}

func lessonIDs(level adminGameLevel) []int64 {
	ids := make([]int64, 0, len(level.Lessons))
	for _, lesson := range level.Lessons {
		ids = append(ids, lesson.ID)
	}
	return ids
}

func adminQuestionIDs(lesson adminGameLesson) []int64 {
	ids := make([]int64, 0, len(lesson.Questions))
	for _, question := range lesson.Questions {
		ids = append(ids, question.ID)
	}
	return ids
}

func TestAnAdminReadsTheCoursesDraftsIncluded(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	code, lessons := importSampleCourse(t, router, 2)
	draft := newCourseCode()
	t.Cleanup(func() { _ = testStore.DeleteGameCourse(context.Background(), draft) })
	created := adminGames(t, router, http.MethodPost, token, "/courses", map[string]any{
		"code": draft, "title": map[string]string{"fr": "  La Bible  ", "en": ""},
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	require.Equal(t, gameText{"fr": "La Bible"}, decode[adminGameCourse](t, created).Title, "trimmed, the empty language dropped")

	member := registerMember(t, router, nil)
	require.Equal(t, http.StatusForbidden, adminGames(t, router, http.MethodGet, member.Token, "/courses", nil).Code)

	list := adminGames(t, router, http.MethodGet, token, "/courses", nil)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	found := map[string]adminGameCourseSummary{}
	for _, course := range decode[[]adminGameCourseSummary](t, list) {
		found[course.Code] = course
	}
	require.True(t, found[code].Published)
	require.Equal(t, 1, found[code].Levels)
	require.Equal(t, 2, found[code].Lessons)
	require.Equal(t, 16, found[code].Questions)
	require.Equal(t, "Our ministry", found[code].Title["en"], "every language comes")
	require.Contains(t, found, draft)
	require.False(t, found[draft].Published)

	course := readCourse(t, router, token, code)
	require.Len(t, course.Levels, 1)
	require.Equal(t, "#0B3B70", course.Levels[0].Color)
	require.True(t, course.Levels[0].Published)
	require.Equal(t, lessons, lessonIDs(course.Levels[0]))
	require.Equal(t, 8, course.Levels[0].Lessons[0].Questions)
	require.Equal(t, 0, course.Levels[0].Lessons[0].Players)

	lesson := readLesson(t, router, token, lessons[0])
	require.Equal(t, code, lesson.Course)
	require.Equal(t, "Notre fondateur", lesson.LevelTitle["fr"])
	require.Len(t, lesson.Questions, 8)
	require.Equal(t, "a", lesson.Questions[0].Answer)
	require.Equal(t, "The right one", lesson.Questions[0].Choices[0].Text["en"])
	require.Equal(t, 1, lesson.Questions[0].Position)

	require.Equal(t, http.StatusNotFound, adminGames(t, router, http.MethodGet, token, "/courses/nothing-here", nil).Code)
	require.Equal(t, http.StatusNotFound, adminGames(t, router, http.MethodGet, token, "/lessons/999999999", nil).Code)
	again := adminGames(t, router, http.MethodPost, token, "/courses", map[string]any{"code": draft, "title": map[string]string{"fr": "Encore"}})
	require.Equal(t, http.StatusConflict, again.Code)
	require.Equal(t, "CONFLICT", errorCode(t, again))
}

func TestACourseIsBuiltPieceByPieceThenPublished(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	code := newCourseCode()
	t.Cleanup(func() { _ = testStore.DeleteGameCourse(context.Background(), code) })
	require.Equal(t, http.StatusCreated, adminGames(t, router, http.MethodPost, token, "/courses",
		map[string]any{"code": code, "title": map[string]string{"fr": "La Bible"}}).Code)

	added := adminGames(t, router, http.MethodPost, token, "/courses/"+code+"/levels", map[string]any{
		"title": map[string]string{"fr": "La création"}, "description": map[string]string{"fr": "Les premiers jours"},
	})
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	course := decode[adminGameCourse](t, added)
	require.Len(t, course.Levels, 1)
	level := course.Levels[0]
	require.False(t, level.Published, "a new level is a draft")
	require.Equal(t, 1, level.Position)
	require.Equal(t, gameDefaultColor, level.Color)

	added = adminGames(t, router, http.MethodPost, token, fmt.Sprintf("/levels/%d/lessons", level.ID),
		map[string]any{"title": map[string]string{"fr": "Le premier jour"}})
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	lessonID := decode[adminGameCourse](t, added).Levels[0].Lessons[0].ID

	publish := func(path string, body map[string]any) *httptest.ResponseRecorder {
		return adminGames(t, router, http.MethodPut, token, path, body)
	}
	lessonBody := map[string]any{"title": map[string]string{"fr": "Le premier jour"}, "published": true}
	levelBody := map[string]any{"title": map[string]string{"fr": "La création"}, "color": "#7A4A98", "published": true}
	courseBody := map[string]any{"title": map[string]string{"fr": "La Bible"}, "published": true}

	for q := 1; q <= 7; q++ {
		response := adminGames(t, router, http.MethodPost, token, fmt.Sprintf("/lessons/%d/questions", lessonID), sampleQuestion(fmt.Sprintf("Question %d ?", q)))
		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	}
	short := publish(fmt.Sprintf("/lessons/%d", lessonID), lessonBody)
	require.Equal(t, http.StatusConflict, short.Code, "7 questions are not a lesson")
	require.Equal(t, "LESSON_TOO_SHORT", errorCode(t, short))
	empty := publish(fmt.Sprintf("/levels/%d", level.ID), levelBody)
	require.Equal(t, http.StatusConflict, empty.Code)
	require.Equal(t, "LEVEL_EMPTY", errorCode(t, empty))
	empty = publish("/courses/"+code, courseBody)
	require.Equal(t, http.StatusConflict, empty.Code)
	require.Equal(t, "COURSE_EMPTY", errorCode(t, empty))

	eighth := adminGames(t, router, http.MethodPost, token, fmt.Sprintf("/lessons/%d/questions", lessonID), sampleQuestion("Question 8 ?"))
	require.Equal(t, http.StatusCreated, eighth.Code)
	lesson := decode[adminGameLesson](t, eighth)
	require.Len(t, lesson.Questions, 8)
	require.Equal(t, 8, lesson.Questions[7].Position)
	require.Equal(t, "Question 8 ?", lesson.Questions[7].Prompt["fr"])

	published := publish(fmt.Sprintf("/lessons/%d", lessonID), lessonBody)
	require.Equal(t, http.StatusOK, published.Code, published.Body.String())
	require.True(t, decode[adminGameLesson](t, published).Published)
	published = publish(fmt.Sprintf("/levels/%d", level.ID), levelBody)
	require.Equal(t, http.StatusOK, published.Code, published.Body.String())
	require.Equal(t, "#7A4A98", decode[adminGameCourse](t, published).Levels[0].Color)
	published = publish("/courses/"+code, courseBody)
	require.Equal(t, http.StatusOK, published.Code, published.Body.String())
	require.True(t, decode[adminGameCourse](t, published).Published)

	// the members see it
	member := registerMember(t, router, nil)
	path := getPath(t, router, member.Token, code)
	require.Equal(t, []string{"open"}, pathStates(path))
}

func TestAQuestionIsCorrectedAndKeepsItsID(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	_, lessons := importSampleCourse(t, router, 1)
	before := readLesson(t, router, token, lessons[0])
	id := before.Questions[2].ID

	question := map[string]any{
		"kind":   "true_false",
		"prompt": map[string]string{"fr": "  Le Pr Fomum est né en 1945.  ", "en": "Prof. Fomum was born in 1945.", "de": "   "},
		"choices": []map[string]any{
			{"id": "true", "text": map[string]string{"fr": "Vrai", "en": "True"}},
			{"id": "false", "text": map[string]string{"fr": "Faux", "en": "False"}},
		},
		"answer":      "true",
		"explanation": map[string]string{"fr": "Il est né le 20 juin 1945."},
		"source":      "ministry:founder",
	}
	path := fmt.Sprintf("/questions/%d", id)
	response := adminGames(t, router, http.MethodPut, token, path, question)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	lesson := decode[adminGameLesson](t, response)
	require.Equal(t, adminQuestionIDs(before), adminQuestionIDs(lesson), "the same questions, in the same order")
	changed := lesson.Questions[2]
	require.Equal(t, "true_false", changed.Kind)
	require.Equal(t, gameText{"fr": "Le Pr Fomum est né en 1945.", "en": "Prof. Fomum was born in 1945."}, changed.Prompt)
	require.Equal(t, "true", changed.Answer)
	require.Equal(t, "True", changed.Choices[0].Text["en"])

	for name, spoil := range map[string]func(q map[string]any){
		"an unknown language":             func(q map[string]any) { q["prompt"] = map[string]string{"fr": "Qui ?", "xx": "Who?"} },
		"no French":                       func(q map[string]any) { q["prompt"] = map[string]string{"en": "Who?"} },
		"an answer not among the choices": func(q map[string]any) { q["answer"] = "maybe" },
		"a prompt too long":               func(q map[string]any) { q["prompt"] = map[string]string{"fr": string(make([]byte, 501))} },
	} {
		broken := map[string]any{}
		for key, value := range question {
			broken[key] = value
		}
		spoil(broken)
		refused := adminGames(t, router, http.MethodPut, token, path, broken)
		require.Equal(t, http.StatusBadRequest, refused.Code, "%s: %s", name, refused.Body.String())
	}
	require.Equal(t, http.StatusNotFound, adminGames(t, router, http.MethodPut, token, "/questions/999999999", question).Code)
}

func TestLevelsLessonsAndQuestionsAreOrdered(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	code, lessons := importSampleCourse(t, router, 3)
	course := readCourse(t, router, token, code)
	first := course.Levels[0]

	// the lessons of a level
	order := fmt.Sprintf("/levels/%d/lessons/order", first.ID)
	response := adminGames(t, router, http.MethodPut, token, order, map[string]any{"ids": []int64{lessons[2], lessons[0], lessons[1]}})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	level := decode[adminGameCourse](t, response).Levels[0]
	require.Equal(t, []int64{lessons[2], lessons[0], lessons[1]}, lessonIDs(level))
	require.Equal(t, []int{1, 2, 3}, []int{level.Lessons[0].Position, level.Lessons[1].Position, level.Lessons[2].Position})

	// a list read before someone else changed it
	for name, ids := range map[string][]int64{
		"one missing":   {lessons[0], lessons[1]},
		"one twice":     {lessons[0], lessons[0], lessons[1]},
		"one of others": {lessons[0], lessons[1], 999999999},
	} {
		stale := adminGames(t, router, http.MethodPut, token, order, map[string]any{"ids": ids})
		require.Equal(t, http.StatusConflict, stale.Code, name)
		require.Equal(t, "ORDER_OUT_OF_DATE", errorCode(t, stale), name)
	}
	require.Equal(t, []int64{lessons[2], lessons[0], lessons[1]}, lessonIDs(readCourse(t, router, token, code).Levels[0]), "nothing moved")

	// the levels of a course
	added := adminGames(t, router, http.MethodPost, token, "/courses/"+code+"/levels", map[string]any{"title": map[string]string{"fr": "Notre but"}})
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	second := decode[adminGameCourse](t, added).Levels[1]
	require.Equal(t, 2, second.Position)
	response = adminGames(t, router, http.MethodPut, token, "/courses/"+code+"/levels/order", map[string]any{"ids": []int64{second.ID, first.ID}})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	levels := decode[adminGameCourse](t, response).Levels
	require.Equal(t, []int64{second.ID, first.ID}, []int64{levels[0].ID, levels[1].ID})
	require.Equal(t, []int{1, 2}, []int{levels[0].Position, levels[1].Position})

	// the questions of a lesson
	lesson := readLesson(t, router, token, lessons[0])
	ids := adminQuestionIDs(lesson)
	ids[0], ids[7] = ids[7], ids[0]
	response = adminGames(t, router, http.MethodPut, token, fmt.Sprintf("/lessons/%d/questions/order", lessons[0]), map[string]any{"ids": ids})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	moved := decode[adminGameLesson](t, response)
	require.Equal(t, ids, adminQuestionIDs(moved))
	require.Equal(t, 1, moved.Questions[0].Position)
	require.Equal(t, 8, moved.Questions[7].Position)
}

func TestWhatMembersPlayedIsNotDeleted(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	code, lessons := importSampleCourse(t, router, 3)
	setGameClock(t, november(12, 10, 0))
	member := registerMember(t, router, nil)
	playLesson(t, router, member.Token, lessons[0], "play-1", "JJJJJJJJ")

	course := readCourse(t, router, token, code)
	require.Equal(t, 1, course.Levels[0].Lessons[0].Players)

	played := adminGames(t, router, http.MethodDelete, token, fmt.Sprintf("/lessons/%d", lessons[0]), nil)
	require.Equal(t, http.StatusConflict, played.Code)
	require.Equal(t, "GAME_CONTENT_PLAYED", errorCode(t, played))
	played = adminGames(t, router, http.MethodDelete, token, fmt.Sprintf("/levels/%d", course.Levels[0].ID), nil)
	require.Equal(t, http.StatusConflict, played.Code)
	require.Equal(t, "GAME_CONTENT_PLAYED", errorCode(t, played))

	// a lesson nobody played goes
	gone := adminGames(t, router, http.MethodDelete, token, fmt.Sprintf("/lessons/%d", lessons[2]), nil)
	require.Equal(t, http.StatusOK, gone.Code, gone.Body.String())
	require.Equal(t, []int64{lessons[0], lessons[1]}, lessonIDs(decode[adminGameCourse](t, gone).Levels[0]))

	// a played lesson can leave the path: it becomes a draft again
	hidden := adminGames(t, router, http.MethodPut, token, fmt.Sprintf("/lessons/%d", lessons[0]),
		map[string]any{"title": map[string]string{"fr": "Leçon 1"}, "published": false})
	require.Equal(t, http.StatusOK, hidden.Code, hidden.Body.String())
	require.False(t, decode[adminGameLesson](t, hidden).Published)
	path := getPath(t, router, member.Token, code)
	require.Len(t, path.Levels[0].Lessons, 1)
	require.Equal(t, lessons[1], path.Levels[0].Lessons[0].ID)

	// a level nobody played goes, with its lessons
	added := adminGames(t, router, http.MethodPost, token, "/courses/"+code+"/levels", map[string]any{"title": map[string]string{"fr": "Notre but"}})
	second := decode[adminGameCourse](t, added).Levels[1]
	gone = adminGames(t, router, http.MethodDelete, token, fmt.Sprintf("/levels/%d", second.ID), nil)
	require.Equal(t, http.StatusOK, gone.Code, gone.Body.String())
	require.Len(t, decode[adminGameCourse](t, gone).Levels, 1)
}

func TestAQuestionLeavesAPublishedLessonOnlyIfEightRemain(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	_, lessons := importSampleCourse(t, router, 1)
	lesson := readLesson(t, router, token, lessons[0])
	path := fmt.Sprintf("/questions/%d", lesson.Questions[0].ID)

	refused := adminGames(t, router, http.MethodDelete, token, path, nil)
	require.Equal(t, http.StatusConflict, refused.Code)
	require.Equal(t, "LESSON_TOO_SHORT", errorCode(t, refused))

	// with a ninth question, one may go
	added := adminGames(t, router, http.MethodPost, token, fmt.Sprintf("/lessons/%d/questions", lessons[0]), sampleQuestion("Une de plus ?"))
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	gone := adminGames(t, router, http.MethodDelete, token, path, nil)
	require.Equal(t, http.StatusOK, gone.Code, gone.Body.String())
	left := decode[adminGameLesson](t, gone)
	require.Len(t, left.Questions, 8)
	require.NotContains(t, adminQuestionIDs(left), lesson.Questions[0].ID)
}

func TestAPlayStillCountsWhenAQuestionLeftTheLessonMeanwhile(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	_, lessons := importSampleCourse(t, router, 1)
	setGameClock(t, november(12, 10, 0))
	added := adminGames(t, router, http.MethodPost, token, fmt.Sprintf("/lessons/%d/questions", lessons[0]), sampleQuestion("Une de plus ?"))
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())

	// the member opens the lesson of 9 questions, then an admin takes one out
	member := registerMember(t, router, nil)
	answers := lessonAnswers(t, router, member.Token, lessons[0], "JJJJJJJJJ")
	gone := answers[0]["question_id"].(int64)
	deleted := adminGames(t, router, http.MethodDelete, token, fmt.Sprintf("/questions/%d", gone), nil)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())

	response := sendLesson(t, router, member.Token, lessons[0], "play-1", answers)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	result := decode[gameResultResponse](t, response)
	require.Equal(t, 8, result.Total, "the question gone is not counted")
	require.Equal(t, 8, result.FirstTry)
	require.Equal(t, 3, result.Stars)
}
