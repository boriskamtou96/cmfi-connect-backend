package api

import (
	"context"
	"encoding/json"
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
	require.Subset(t, names, []string{"ministry.fr.json", "bible.fr.json", "versets.fr.json", "priere.fr.json", "quiz.fr.json"})
}
