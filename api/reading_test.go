package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// itemID is the id of an item of the member's day form, by its code.
func itemID(t *testing.T, router http.Handler, token, code string) int64 {
	form := decode[dailyReportFormResponse](t, send(t, router, http.MethodGet, token, "/v1/reports/2026-11-02", nil))
	for _, entry := range form.Entries {
		if entry.Code == code {
			return entry.ActivityTypeID
		}
	}
	t.Fatalf("no %s item in the day form", code)
	return 0
}

func TestABookFollowsThePagesOfTheReports(t *testing.T) {
	router := newTestRouter(t)
	m := registerMember(t, router, nil)
	llc := itemID(t, router, m.Token, "LLC")

	created := send(t, router, http.MethodPost, m.Token, "/v1/reading/books", map[string]any{
		"title": "  Le chemin de l'obéissance ", "author": "Z. T. Fomum", "total_pages": 100,
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	book := decode[readingBookResponse](t, created)
	require.Equal(t, "Le chemin de l'obéissance", book.Title)
	require.Equal(t, bookReading, book.Status)
	require.Zero(t, book.PagesRead)

	// two days of reading, the second one with a duration too
	for _, day := range []struct {
		date  string
		pages int
	}{{"2026-11-03", 30}, {"2026-11-04", 50}} {
		saved := send(t, router, http.MethodPut, m.Token, "/v1/reports/"+day.date, map[string]any{
			"entries": []map[string]any{{"activity_type_id": llc, "quantity": day.pages, "duration_minutes": 25, "book_id": book.ID}},
		})
		require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
		form := decode[dailyReportFormResponse](t, saved)
		for _, entry := range form.Entries {
			if entry.Code == "LLC" {
				require.NotNil(t, entry.Book)
				require.Equal(t, book.ID, entry.Book.ID)
				require.Equal(t, int32(100), entry.Book.TotalPages)
			} else {
				require.Nil(t, entry.Book)
			}
		}
	}

	books := decode[[]readingBookResponse](t, send(t, router, http.MethodGet, m.Token, "/v1/reading/books", nil))
	require.Len(t, books, 1)
	require.Equal(t, int32(80), books[0].PagesRead)
	require.Equal(t, "2026-11-03", books[0].StartedOn)

	// the history tells the page reached each day
	history := decode[[]dailyReportHistoryDay](t, send(t, router, http.MethodGet, m.Token,
		"/v1/reports/?from=2026-11-01&to=2026-11-05", nil))
	require.Len(t, history, 2)
	require.Equal(t, int32(80), *history[0].Entries[0].Book.PageReached)
	require.Equal(t, int32(30), *history[1].Entries[0].Book.PageReached)

	// finished: it counts in its month
	finished := send(t, router, http.MethodPut, m.Token, "/v1/reading/books/"+strconv.FormatInt(book.ID, 10)+"/status",
		map[string]any{"status": "finished", "finished_on": "2026-11-05"})
	require.Equal(t, http.StatusOK, finished.Code, finished.Body.String())
	require.Equal(t, "2026-11-05", *decode[readingBookResponse](t, finished).FinishedOn)

	stats := decode[readingStatsResponse](t, send(t, router, http.MethodGet, m.Token, "/v1/reading/stats?year=2026", nil))
	require.Len(t, stats.Months, 12)
	require.Equal(t, "2026-11", stats.Months[10].Month)
	require.Equal(t, int32(1), stats.Months[10].Books)
	require.Equal(t, int32(1), stats.BooksFinished)
	require.Equal(t, int32(80), stats.Pages)

	// read again: no longer finished
	again := send(t, router, http.MethodPut, m.Token, "/v1/reading/books/"+strconv.FormatInt(book.ID, 10)+"/status", map[string]any{"status": "reading"})
	require.Equal(t, http.StatusOK, again.Code, again.Body.String())
	require.Nil(t, decode[readingBookResponse](t, again).FinishedOn)

	// deleted: the reports keep their pages, without the book
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, m.Token, "/v1/reading/books/"+strconv.FormatInt(book.ID, 10), nil).Code)
	form := decode[dailyReportFormResponse](t, send(t, router, http.MethodGet, m.Token, "/v1/reports/2026-11-03", nil))
	for _, entry := range form.Entries {
		if entry.Code == "LLC" {
			require.Nil(t, entry.Book)
			require.Equal(t, int32(30), *entry.Quantity)
		}
	}
}

func TestABookIsItsMembersOwnAndOnlyForTheLLCItem(t *testing.T) {
	router := newTestRouter(t)
	owner := registerMember(t, router, nil)
	other := registerMember(t, router, nil)
	book := decode[readingBookResponse](t, send(t, router, http.MethodPost, owner.Token, "/v1/reading/books",
		map[string]any{"title": "La vie de prière", "total_pages": 180}))

	// someone else's book can't be named in a report
	llc := itemID(t, router, other.Token, "LLC")
	refused := send(t, router, http.MethodPut, other.Token, "/v1/reports/2026-11-03", map[string]any{
		"entries": []map[string]any{{"activity_type_id": llc, "quantity": 10, "book_id": book.ID}},
	})
	require.Equal(t, http.StatusBadRequest, refused.Code, refused.Body.String())

	// nor changed or deleted by them
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPut, other.Token, "/v1/reading/books/"+strconv.FormatInt(book.ID, 10),
		map[string]any{"title": "Autre", "total_pages": 10}).Code)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPut, other.Token, "/v1/reading/books/"+strconv.FormatInt(book.ID, 10)+"/status",
		map[string]any{"status": "finished"}).Code)
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, other.Token, "/v1/reading/books/"+strconv.FormatInt(book.ID, 10), nil).Code)
	require.Len(t, decode[[]readingBookResponse](t, send(t, router, http.MethodGet, owner.Token, "/v1/reading/books", nil)), 1)
	require.Empty(t, decode[[]readingBookResponse](t, send(t, router, http.MethodGet, other.Token, "/v1/reading/books", nil)))

	// a book goes with the LLC item only
	lb := itemID(t, router, owner.Token, "LB")
	wrongItem := send(t, router, http.MethodPut, owner.Token, "/v1/reports/2026-11-03", map[string]any{
		"entries": []map[string]any{{"activity_type_id": lb, "quantity": 2, "book_id": book.ID}},
	})
	require.Equal(t, http.StatusBadRequest, wrongItem.Code, wrongItem.Body.String())

	// a book is checked
	for _, body := range []map[string]any{
		{"title": "  ", "total_pages": 10},
		{"title": "Sans pages"},
		{"title": "Trop", "total_pages": 30000},
		{"title": "Couverture", "total_pages": 10, "cover_url": "https://example.com/x.jpg"},
	} {
		response := send(t, router, http.MethodPost, owner.Token, "/v1/reading/books", body)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	}
	future := send(t, router, http.MethodPut, owner.Token, "/v1/reading/books/"+strconv.FormatInt(book.ID, 10)+"/status",
		map[string]any{"status": "finished", "finished_on": "2999-01-01"})
	require.Equal(t, http.StatusBadRequest, future.Code, future.Body.String())
}

func TestBooksAreFoundOnOpenLibrary(t *testing.T) {
	library := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "chemin obeissance", r.URL.Query().Get("q"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"docs": [
			{"title": "The Way of Obedience", "author_name": ["Zacharias Tanee Fomum"], "number_of_pages_median": 112, "cover_i": 42},
			{"title": "", "author_name": ["Nobody"]},
			{"title": "Sans pages"}
		]}`))
	}))
	defer library.Close()
	previous := openLibrarySearchURL
	openLibrarySearchURL = library.URL
	t.Cleanup(func() { openLibrarySearchURL = previous })

	router := newTestRouter(t)
	m := registerMember(t, router, nil)
	found := send(t, router, http.MethodGet, m.Token, "/v1/reading/search?q=chemin+obeissance", nil)
	require.Equal(t, http.StatusOK, found.Code, found.Body.String())
	books := decode[[]bookFound](t, found)
	require.Len(t, books, 2)
	require.Equal(t, bookFound{
		Title: "The Way of Obedience", Author: "Zacharias Tanee Fomum", TotalPages: 112,
		CoverURL: "https://covers.openlibrary.org/b/id/42-M.jpg",
	}, books[0])
	require.Zero(t, books[1].TotalPages)

	// out of reach: the member types the book
	openLibrarySearchURL = "http://127.0.0.1:1/search.json"
	down := send(t, router, http.MethodGet, m.Token, "/v1/reading/search?q=chemin", nil)
	require.Equal(t, http.StatusServiceUnavailable, down.Code)
	require.Equal(t, "SEARCH_UNAVAILABLE", errorCode(t, down))
}
