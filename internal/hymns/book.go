// Package hymns describes a hymn book as stored in data/hymns/*.json
// (made by tools/hymns/extract_pdf.py) and in the hymns.parts column.
package hymns

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Kinds of the parts of a hymn.
const (
	KindVerse  = "verse"
	KindChorus = "chorus"
)

// ErrInvalidBook wraps every "this file is not a valid hymn book" error.
var ErrInvalidBook = errors.New("invalid hymn book")

var bookCodePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Part is a verse or a chorus. Label is the number printed before it: "1" for the
// first verse, "2" for "Refrain 2"; empty for a chorus without number.
// Repeat is how many times it is sung ("Refrain : x2" gives 2); 0 means once.
type Part struct {
	Kind   string   `json:"kind"`
	Label  string   `json:"label,omitempty"`
	Repeat int32    `json:"repeat,omitempty"`
	Lines  []string `json:"lines"`
}

type Hymn struct {
	Number int32  `json:"number"`
	Title  string `json:"title"`
	// Author is the words / music credit printed under the hymn, if any.
	Author string `json:"author,omitempty"`
	Parts  []Part `json:"parts"`
}

type Book struct {
	// Code identifies the book in URLs and imports: "rendons-ministere-1".
	Code     string `json:"code"`
	Title    string `json:"title"`
	Language string `json:"language"`
	// Position orders the books in the app.
	Position int32 `json:"position"`
}

// File is the content of one data/hymns/*.json file: a book and all its hymns.
type File struct {
	Book  Book   `json:"book"`
	Hymns []Hymn `json:"hymns"`
}

// Load reads and validates a hymn book file.
func Load(path string) (File, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}

	var file File
	if err := json.Unmarshal(content, &file); err != nil {
		return File{}, fmt.Errorf("%w: %s: %v", ErrInvalidBook, path, err)
	}
	if err := file.Validate(); err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	return file, nil
}

// Validate checks what the database and the apps rely on: a code usable in a URL,
// unique positive numbers, and hymns that all have text.
func (f File) Validate() error {
	if !bookCodePattern.MatchString(f.Book.Code) || len(f.Book.Code) > 50 {
		return fmt.Errorf("%w: book code %q must be lowercase letters, digits and dashes", ErrInvalidBook, f.Book.Code)
	}
	if strings.TrimSpace(f.Book.Title) == "" {
		return fmt.Errorf("%w: book %s has no title", ErrInvalidBook, f.Book.Code)
	}
	if language := strings.TrimSpace(f.Book.Language); language == "" || len(language) > 10 {
		return fmt.Errorf("%w: book %s needs a language such as \"fr\" or \"en\"", ErrInvalidBook, f.Book.Code)
	}
	if len(f.Hymns) == 0 {
		return fmt.Errorf("%w: book %s has no hymns", ErrInvalidBook, f.Book.Code)
	}

	seen := make(map[int32]bool, len(f.Hymns))
	for _, hymn := range f.Hymns {
		if hymn.Number <= 0 {
			return fmt.Errorf("%w: hymn number %d must be positive", ErrInvalidBook, hymn.Number)
		}
		if seen[hymn.Number] {
			return fmt.Errorf("%w: hymn %d appears twice", ErrInvalidBook, hymn.Number)
		}
		seen[hymn.Number] = true

		if strings.TrimSpace(hymn.Title) == "" {
			return fmt.Errorf("%w: hymn %d has no title", ErrInvalidBook, hymn.Number)
		}
		if len(hymn.Parts) == 0 {
			return fmt.Errorf("%w: hymn %d has no verse", ErrInvalidBook, hymn.Number)
		}
		for i, part := range hymn.Parts {
			if part.Kind != KindVerse && part.Kind != KindChorus {
				return fmt.Errorf("%w: hymn %d, part %d: unknown kind %q", ErrInvalidBook, hymn.Number, i+1, part.Kind)
			}
			if len(part.Lines) == 0 {
				return fmt.Errorf("%w: hymn %d, part %d has no line", ErrInvalidBook, hymn.Number, i+1)
			}
			if part.Repeat < 0 || part.Repeat > 10 {
				return fmt.Errorf("%w: hymn %d, part %d is repeated %d times", ErrInvalidBook, hymn.Number, i+1, part.Repeat)
			}
			for _, line := range part.Lines {
				if strings.TrimSpace(line) == "" {
					return fmt.Errorf("%w: hymn %d, part %d has an empty line", ErrInvalidBook, hymn.Number, i+1)
				}
			}
		}
	}
	return nil
}

// FirstLine is the first line of the first verse: what people remember of a hymn.
func FirstLine(parts []Part) string {
	for _, part := range parts {
		if len(part.Lines) > 0 {
			return part.Lines[0]
		}
	}
	return ""
}
