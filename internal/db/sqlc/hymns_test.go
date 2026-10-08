package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/hymns"
	"github.com/go-faker/faker/v4"
	"github.com/stretchr/testify/require"
)

// testHymnBook is a small book with its own code, deleted at the end of the test.
func testHymnBook(t *testing.T) hymns.File {
	code := "test-" + strings.ToLower(faker.Username())
	t.Cleanup(func() {
		book, err := testQueries.GetHymnBookByCode(context.Background(), code)
		if err == nil {
			_ = testQueries.DeleteHymnBook(context.Background(), book.ID)
		}
	})

	return hymns.File{
		Book: hymns.Book{Code: code, Title: "Recueil de test", Language: "fr", Position: 99},
		Hymns: []hymns.Hymn{
			{Number: 1, Title: "A Toi la gloire", Author: "EMPEYTAZ.", Parts: []hymns.Part{
				{Kind: hymns.KindVerse, Label: "1", Lines: []string{"À toi la gloire", "Ô Ressuscité"}},
				{Kind: hymns.KindChorus, Lines: []string{"À toi la victoire", "Pour l'éternité"}},
			}},
			{Number: 2, Title: "Saint, Saint, Saint", Parts: []hymns.Part{
				{Kind: hymns.KindVerse, Label: "1", Lines: []string{"Saint ! Saint ! Saint ! est l'Eternel"}},
			}},
			{Number: 3, Title: "Foi de nos pères", Parts: []hymns.Part{
				{Kind: hymns.KindVerse, Label: "1", Lines: []string{"Foi de nos pères et notre aussi,"}},
			}},
		},
	}
}

func testStore() *SQLStore {
	return NewSQLStore(testQueries.db.(*sql.DB))
}

func TestImportHymnBookTxCreatesTheBook(t *testing.T) {
	file := testHymnBook(t)

	result, err := testStore().ImportHymnBookTx(context.Background(), file, false)
	require.NoError(t, err)

	require.True(t, result.BookCreated)
	require.Equal(t, 3, result.Inserted)
	require.Zero(t, result.Updated)
	require.Equal(t, file.Book.Code, result.Book.Code)

	row, err := testQueries.GetHymn(context.Background(), GetHymnParams{BookID: result.Book.ID, Number: 1})
	require.NoError(t, err)
	require.Equal(t, "A Toi la gloire", row.Title)
	require.Equal(t, "EMPEYTAZ.", row.Author)

	var parts []hymns.Part
	require.NoError(t, json.Unmarshal(row.Parts, &parts))
	require.Equal(t, file.Hymns[0].Parts, parts)
}

func TestImportHymnBookTxTwiceChangesNothing(t *testing.T) {
	file := testHymnBook(t)
	store := testStore()

	first, err := store.ImportHymnBookTx(context.Background(), file, false)
	require.NoError(t, err)

	again, err := store.ImportHymnBookTx(context.Background(), file, false)
	require.NoError(t, err)

	require.False(t, again.Changed())
	require.Equal(t, 3, again.Unchanged)
	// same version: the apps keep their copy
	require.True(t, first.Book.UpdatedAt.Equal(again.Book.UpdatedAt))
}

func TestImportHymnBookTxUpdatesAndPrunes(t *testing.T) {
	file := testHymnBook(t)
	store := testStore()

	first, err := store.ImportHymnBookTx(context.Background(), file, false)
	require.NoError(t, err)

	// another author for hymn 1, a typo fixed in hymn 2, hymn 3 removed from the file, hymn 4 added
	file.Hymns[0].Author = "C. MALAN"
	file.Hymns[1].Parts[0].Lines[0] = "Saint ! Saint ! Saint ! est l'Éternel"
	file.Hymns = append(file.Hymns[:2], hymns.Hymn{Number: 4, Title: "Nouveau", Parts: []hymns.Part{
		{Kind: hymns.KindVerse, Label: "1", Lines: []string{"Une ligne"}},
	}})

	result, err := store.ImportHymnBookTx(context.Background(), file, true)
	require.NoError(t, err)

	require.Equal(t, 1, result.Inserted)
	require.Equal(t, 2, result.Updated)
	require.Equal(t, 0, result.Unchanged)
	require.Equal(t, 1, result.Deleted)
	require.True(t, result.Book.UpdatedAt.After(first.Book.UpdatedAt), "a new version")

	rows, err := testQueries.ListBookHymns(context.Background(), result.Book.ID)
	require.NoError(t, err)
	numbers := make([]int32, 0, len(rows))
	for _, row := range rows {
		numbers = append(numbers, row.Number)
	}
	require.Equal(t, []int32{1, 2, 4}, numbers)
}

func TestImportHymnBookTxWithoutPruneKeepsOtherHymns(t *testing.T) {
	file := testHymnBook(t)
	store := testStore()

	_, err := store.ImportHymnBookTx(context.Background(), file, false)
	require.NoError(t, err)

	file.Hymns = file.Hymns[:1]
	result, err := store.ImportHymnBookTx(context.Background(), file, false)
	require.NoError(t, err)
	require.Zero(t, result.Deleted)

	rows, err := testQueries.ListBookHymns(context.Background(), result.Book.ID)
	require.NoError(t, err)
	require.Len(t, rows, 3)
}

func TestImportHymnBookTxUpdatesTheBookTitle(t *testing.T) {
	file := testHymnBook(t)
	store := testStore()

	_, err := store.ImportHymnBookTx(context.Background(), file, false)
	require.NoError(t, err)

	file.Book.Title = "Recueil de test, volume 2"
	result, err := store.ImportHymnBookTx(context.Background(), file, false)
	require.NoError(t, err)

	require.True(t, result.BookUpdated)
	require.True(t, result.Changed())
	require.Equal(t, "Recueil de test, volume 2", result.Book.Title)
}

func TestImportHymnBookTxRejectsAnInvalidFile(t *testing.T) {
	file := testHymnBook(t)
	file.Hymns[1].Number = 1

	_, err := testStore().ImportHymnBookTx(context.Background(), file, false)
	require.True(t, errors.Is(err, hymns.ErrInvalidBook))

	_, err = testQueries.GetHymnBookByCode(context.Background(), file.Book.Code)
	require.ErrorIs(t, err, sql.ErrNoRows, "nothing written")
}

func TestListHymnBooksCountsTheHymns(t *testing.T) {
	file := testHymnBook(t)
	_, err := testStore().ImportHymnBookTx(context.Background(), file, false)
	require.NoError(t, err)

	books, err := testQueries.ListHymnBooks(context.Background())
	require.NoError(t, err)

	for _, book := range books {
		if book.Code == file.Book.Code {
			require.Equal(t, int64(3), book.HymnCount)
			return
		}
	}
	t.Fatalf("book %s not listed", file.Book.Code)
}
