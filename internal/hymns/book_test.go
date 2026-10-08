package hymns

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func validFile() File {
	return File{
		Book: Book{Code: "rendons-ministere-1", Title: "Rendons ministère au Seigneur, volume 1", Language: "fr"},
		Hymns: []Hymn{
			{Number: 1, Title: "A Toi la gloire", Parts: []Part{
				{Kind: KindVerse, Label: "1", Lines: []string{"À toi la gloire", "Ô Ressuscité"}},
				{Kind: KindChorus, Repeat: 2, Lines: []string{"À toi la gloire"}},
			}},
			{Number: 2, Title: "Saint, Saint, Saint", Parts: []Part{
				{Kind: KindVerse, Label: "1", Lines: []string{"Saint ! Saint ! Saint ! est l'Eternel"}},
			}},
		},
	}
}

func TestValidateAcceptsABook(t *testing.T) {
	require.NoError(t, validFile().Validate())
}

func TestValidateRejectsWhatTheAppsCannotShow(t *testing.T) {
	cases := map[string]func(f *File){
		"code with spaces":   func(f *File) { f.Book.Code = "Rendons ministère" },
		"no book title":      func(f *File) { f.Book.Title = " " },
		"no language":        func(f *File) { f.Book.Language = "" },
		"no hymns":           func(f *File) { f.Hymns = nil },
		"number zero":        func(f *File) { f.Hymns[0].Number = 0 },
		"duplicate number":   func(f *File) { f.Hymns[1].Number = 1 },
		"no hymn title":      func(f *File) { f.Hymns[0].Title = "" },
		"no parts":           func(f *File) { f.Hymns[0].Parts = nil },
		"unknown part kind":  func(f *File) { f.Hymns[0].Parts[0].Kind = "bridge" },
		"part without lines": func(f *File) { f.Hymns[0].Parts[1].Lines = nil },
		"empty line":         func(f *File) { f.Hymns[0].Parts[0].Lines[1] = "  " },
		"negative repeat":    func(f *File) { f.Hymns[0].Parts[1].Repeat = -1 },
	}

	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			file := validFile()
			breakIt(&file)

			err := file.Validate()
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrInvalidBook))
		})
	}
}

func TestLoadReadsAndValidatesAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.json")
	content, err := json.Marshal(validFile())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, content, 0o600))

	file, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "rendons-ministere-1", file.Book.Code)
	require.Len(t, file.Hymns, 2)

	require.NoError(t, os.WriteFile(path, []byte(`{"book": {"code": "x"}`), 0o600))
	_, err = Load(path)
	require.True(t, errors.Is(err, ErrInvalidBook))
}

func TestFirstLine(t *testing.T) {
	require.Equal(t, "À toi la gloire", FirstLine(validFile().Hymns[0].Parts))
	require.Equal(t, "", FirstLine(nil))
}
