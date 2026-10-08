package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/hymns"
)

// ImportHymnBookResult says what an import changed. A book whose content did not
// change keeps its version (hymn_books.updated_at), so the apps don't download it again.
type ImportHymnBookResult struct {
	Book        HymnBook
	BookCreated bool
	// BookUpdated: the title, language or position changed.
	BookUpdated bool
	Inserted    int
	Updated     int
	Unchanged   int
	Deleted     int
}

// Changed is true when the apps must download the book again.
func (r ImportHymnBookResult) Changed() bool {
	return r.BookCreated || r.BookUpdated || r.Inserted > 0 || r.Updated > 0 || r.Deleted > 0
}

// ImportHymnBookTx creates or updates a book and all its hymns in one transaction.
// With prune, the hymns of the book that are not in the file are deleted.
// The file is validated first: an invalid file returns an error wrapping hymns.ErrInvalidBook.
func (s *SQLStore) ImportHymnBookTx(ctx context.Context, file hymns.File, prune bool) (ImportHymnBookResult, error) {
	var result ImportHymnBookResult

	if err := file.Validate(); err != nil {
		return result, err
	}

	err := s.execTx(ctx, func(q *Queries) error {
		// 1. the book: created, or its title / language / position updated
		book, err := q.GetHymnBookByCode(ctx, file.Book.Code)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			book, err = q.CreateHymnBook(ctx, CreateHymnBookParams{
				Code:     file.Book.Code,
				Title:    file.Book.Title,
				Language: file.Book.Language,
				Position: file.Book.Position,
			})
			if err != nil {
				return err
			}
			result.BookCreated = true
		case err != nil:
			return err
		default:
			rows, err := q.UpdateHymnBook(ctx, UpdateHymnBookParams{
				ID:       book.ID,
				Title:    file.Book.Title,
				Language: file.Book.Language,
				Position: file.Book.Position,
			})
			if err != nil {
				return err
			}
			result.BookUpdated = rows > 0
		}

		// 2. the hymns: inserted, updated when their text changed, or left as they are
		existing, err := q.ListHymnNumbers(ctx, book.ID)
		if err != nil {
			return err
		}
		known := make(map[int32]bool, len(existing))
		for _, number := range existing {
			known[number] = true
		}

		numbers := make([]int32, 0, len(file.Hymns))
		for _, hymn := range file.Hymns {
			parts, err := json.Marshal(hymn.Parts)
			if err != nil {
				return err
			}
			rows, err := q.UpsertHymn(ctx, UpsertHymnParams{
				BookID: book.ID,
				Number: hymn.Number,
				Title:  hymn.Title,
				Author: hymn.Author,
				Parts:  parts,
			})
			if err != nil {
				return err
			}
			switch {
			case rows == 0:
				result.Unchanged++
			case known[hymn.Number]:
				result.Updated++
			default:
				result.Inserted++
			}
			numbers = append(numbers, hymn.Number)
		}

		// 3. the hymns removed from the file
		if prune {
			deleted, err := q.DeleteHymnsNotIn(ctx, DeleteHymnsNotInParams{BookID: book.ID, Numbers: numbers})
			if err != nil {
				return err
			}
			result.Deleted = int(deleted)
		}

		// 4. a new version when anything changed
		if result.Changed() {
			if err := q.TouchHymnBook(ctx, book.ID); err != nil {
				return err
			}
		}
		result.Book, err = q.GetHymnBookByCode(ctx, file.Book.Code)
		return err
	})

	return result, err
}
