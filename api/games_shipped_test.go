package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/boriskamtou96/cmfi-connect-backend/data/games"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/stretchr/testify/require"
)

// shippedCourse is a course file as data/games holds them, of [code].
func shippedCourse(t *testing.T, code, title string) []byte {
	raw, err := json.Marshal(map[string]any{
		"code": code, "position": 9, "title": map[string]string{"fr": title}, "published": true,
		"levels": []map[string]any{{
			"title":   map[string]string{"fr": "Niveau 1"},
			"lessons": []map[string]any{{"title": map[string]string{"fr": "Leçon 1"}, "questions": sampleQuestions(8)}},
		}},
	})
	require.NoError(t, err)
	return raw
}

func sampleQuestions(n int) []map[string]any {
	questions := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		questions = append(questions, sampleQuestion("Question "+string(rune('A'+i))+" ?"))
	}
	return questions
}

func TestTheCoursesShippedAreImportedAtStartWhenTheyAreMissing(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	server, err := New(testStore, &utils.Config{Token: utils.TokenConfig{SecretKey: strings.Repeat("k", 32), Duration: "15m"}})
	require.NoError(t, err)
	fresh, kept := newCourseCode(), newCourseCode()
	t.Cleanup(func() {
		_ = testStore.DeleteGameCourse(context.Background(), fresh)
		_ = testStore.DeleteGameCourse(context.Background(), kept)
	})
	// a course the admins already have, and changed since
	require.Equal(t, 201, adminGames(t, router, "POST", token, "/courses", map[string]any{
		"code": kept, "title": map[string]string{"fr": "Renommé par un admin"},
	}).Code)

	files := fstest.MapFS{
		fresh + ".fr.json": {Data: shippedCourse(t, fresh, "Nouveau parcours")},
		kept + ".fr.json":  {Data: shippedCourse(t, kept, "Titre du fichier")},
		"broken.fr.json":   {Data: []byte(`{"code": "x", "levels": [`)},
	}
	err = server.ImportShippedGameCourses(context.Background(), files)
	require.Error(t, err, "the broken file is said")

	imported := readCourse(t, router, token, fresh)
	require.True(t, imported.Published)
	require.Len(t, imported.Levels, 1)
	require.Equal(t, 8, imported.Levels[0].Lessons[0].Questions)
	// never over a course that exists
	require.Equal(t, "Renommé par un admin", readCourse(t, router, token, kept).Title["fr"])

	// started again: nothing changes
	require.Error(t, server.ImportShippedGameCourses(context.Background(), files))
	require.Len(t, readCourse(t, router, token, fresh).Levels, 1)
}

func TestTheServerShipsItsCourses(t *testing.T) {
	entries, err := games.Courses.ReadDir(".")
	require.NoError(t, err)
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	require.Subset(t, names, []string{"ministry.fr.json", "bible.fr.json", "versets.fr.json", "priere.fr.json", "quiz.fr.json", "parcours.fr.json"})
}

// Every course shipped with the server reads and passes the rules of an
// import: a file that fails would be left out at start, without a word to
// the members.
func TestEveryShippedCourseIsValid(t *testing.T) {
	entries, err := games.Courses.ReadDir(".")
	require.NoError(t, err)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := games.Courses.ReadFile(entry.Name())
		require.NoError(t, err)
		var course importGameCourseRequest
		require.NoError(t, json.Unmarshal(raw, &course), entry.Name())
		require.NoError(t, course.validate(), entry.Name())
	}
}

// twoLevelCourse is [code] with « Niveau 1 » (renamed or not in the
// database) and « Niveau 2 », added to the file since.
func twoLevelCourse(t *testing.T, code string) []byte {
	level := func(n int) map[string]any {
		return map[string]any{
			"title":   map[string]string{"fr": fmt.Sprintf("Niveau %d", n)},
			"lessons": []map[string]any{{"title": map[string]string{"fr": fmt.Sprintf("Leçon %d", n)}, "questions": sampleQuestions(8)}},
		}
	}
	raw, err := json.Marshal(map[string]any{
		"code": code, "position": 9, "title": map[string]string{"fr": "Parcours"}, "published": true,
		"levels": []map[string]any{level(1), level(2)},
	})
	require.NoError(t, err)
	return raw
}

func TestTheLevelsAddedToAShippedCourseReachTheDatabaseOnce(t *testing.T) {
	router := newTestRouter(t)
	token := adminToken(t, router)
	server, err := New(testStore, &utils.Config{Token: utils.TokenConfig{SecretKey: strings.Repeat("k", 32), Duration: "15m"}})
	require.NoError(t, err)
	code := newCourseCode()
	t.Cleanup(func() { _ = testStore.DeleteGameCourse(context.Background(), code) })

	// the course as it was first shipped, then its first level renamed by
	// an admin in the back-office
	first := fstest.MapFS{code + ".fr.json": {Data: shippedCourse(t, code, "Parcours")}}
	require.NoError(t, server.ImportShippedGameCourses(context.Background(), first))
	level := readCourse(t, router, token, code).Levels[0]
	renamed := adminGames(t, router, http.MethodPut, token, fmt.Sprintf("/levels/%d", level.ID), map[string]any{
		"title": map[string]string{"fr": "Les débuts"}, "color": "#7A4A98", "published": true,
	})
	require.Equal(t, http.StatusOK, renamed.Code, renamed.Body.String())

	// the file has a new level since: it comes after, published, and the
	// renamed one stays as the admin left it, not added again
	later := fstest.MapFS{code + ".fr.json": {Data: twoLevelCourse(t, code)}}
	require.NoError(t, server.ImportShippedGameCourses(context.Background(), later))
	course := readCourse(t, router, token, code)
	require.Len(t, course.Levels, 2)
	require.Equal(t, level.ID, course.Levels[0].ID)
	require.Equal(t, "Les débuts", course.Levels[0].Title["fr"])
	require.Equal(t, "Niveau 2", course.Levels[1].Title["fr"])
	require.Equal(t, 2, course.Levels[1].Position)
	require.True(t, course.Levels[1].Published)
	require.Equal(t, 8, course.Levels[1].Lessons[0].Questions)

	// started again: nothing more
	require.NoError(t, server.ImportShippedGameCourses(context.Background(), later))
	require.Len(t, readCourse(t, router, token, code).Levels, 2)
}
