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

	// out of reach, both: the member types the book
	openLibrarySearchURL = "http://127.0.0.1:1/search.json"
	down := send(t, router, http.MethodGet, m.Token, "/v1/reading/search?q=chemin", nil)
	require.Equal(t, http.StatusServiceUnavailable, down.Code)
	require.Equal(t, "SEARCH_UNAVAILABLE", errorCode(t, down))
}

// bookSearchServers starts an Open Library and a Google Books answering these
// bodies, and points the searches at them for the test.
func bookSearchServers(t *testing.T, library, google func(r *http.Request) string) {
	t.Helper()
	serve := func(answer func(r *http.Request) string) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(answer(r)))
		}))
		t.Cleanup(server.Close)
		return server
	}
	previousLibrary, previousGoogle := openLibrarySearchURL, googleBooksURL
	openLibrarySearchURL = serve(library).URL
	googleBooksURL = serve(google).URL
	t.Cleanup(func() { openLibrarySearchURL, googleBooksURL = previousLibrary, previousGoogle })
}

func TestABookTypedByHandGetsItsCover(t *testing.T) {
	bookSearchServers(t,
		func(r *http.Request) string {
			// searched by its title and author
			if r.URL.Query().Get("title") == "Le chemin de l'obéissance" {
				require.Equal(t, "Z. T. Fomum", r.URL.Query().Get("author"))
				return `{"docs": [{"title": "Le chemin de l'obéissance"}, {"title": "Le chemin de l'obéissance", "cover_i": 7}]}`
			}
			return `{"docs": []}`
		},
		func(r *http.Request) string {
			require.Equal(t, `intitle:"La vie de prière" inauthor:"Z. T. Fomum"`, r.URL.Query().Get("q"))
			return `{"items": [{"volumeInfo": {"title": "La vie de prière", "imageLinks": {"thumbnail": "http://books.google.com/books/content?id=AbC_12&printsec=frontcover&img=1&zoom=1&edge=curl&source=gbs_api"}}}]}`
		})

	router := newTestRouter(t)
	m := registerMember(t, router, nil)

	// Open Library has it
	added := send(t, router, http.MethodPost, m.Token, "/v1/reading/books",
		map[string]any{"title": "Le chemin de l'obéissance", "author": "Z. T. Fomum", "total_pages": 240})
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	require.Equal(t, "https://covers.openlibrary.org/b/id/7-M.jpg", decode[readingBookResponse](t, added).CoverURL)

	// else Google Books
	added = send(t, router, http.MethodPost, m.Token, "/v1/reading/books",
		map[string]any{"title": "La vie de prière", "author": "Z. T. Fomum", "total_pages": 180})
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	require.Equal(t, "https://books.google.com/books/content?id=AbC_12&printsec=frontcover&img=1&zoom=1",
		decode[readingBookResponse](t, added).CoverURL)

	// neither: the book has none, the app draws it
	openLibrarySearchURL = "http://127.0.0.1:1/search.json"
	googleBooksURL = "http://127.0.0.1:1/volumes"
	added = send(t, router, http.MethodPost, m.Token, "/v1/reading/books",
		map[string]any{"title": "Un livre introuvable", "total_pages": 90})
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	require.Empty(t, decode[readingBookResponse](t, added).CoverURL)
}

func TestTheSearchAsksGoogleBooksWhenOpenLibraryFindsNothing(t *testing.T) {
	bookSearchServers(t,
		func(*http.Request) string { return `{"docs": []}` },
		func(r *http.Request) string {
			require.Equal(t, "fomum sanctification", r.URL.Query().Get("q"))
			return `{"items": [
				{"volumeInfo": {"title": "Le chemin de la sanctification", "authors": ["Zacharias Tanee Fomum"], "pageCount": 212,
					"imageLinks": {"thumbnail": "http://books.google.com/books/content?id=Xy9&printsec=frontcover&img=1&zoom=1&source=gbs_api"}}},
				{"volumeInfo": {"title": "Sans couverture", "imageLinks": {"thumbnail": "http://example.com/x.jpg"}}},
				{"volumeInfo": {"title": ""}}
			]}`
		})

	router := newTestRouter(t)
	m := registerMember(t, router, nil)
	found := send(t, router, http.MethodGet, m.Token, "/v1/reading/search?q=fomum+sanctification", nil)
	require.Equal(t, http.StatusOK, found.Code, found.Body.String())
	books := decode[[]bookFound](t, found)
	require.Equal(t, []bookFound{
		{
			Title: "Le chemin de la sanctification", Author: "Zacharias Tanee Fomum", TotalPages: 212,
			CoverURL: "https://books.google.com/books/content?id=Xy9&printsec=frontcover&img=1&zoom=1",
		},
		{Title: "Sans couverture"},
	}, books)

	// a cover from Google Books may be kept
	added := send(t, router, http.MethodPost, m.Token, "/v1/reading/books",
		map[string]any{"title": books[0].Title, "total_pages": 212, "cover_url": books[0].CoverURL})
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	require.Equal(t, books[0].CoverURL, decode[readingBookResponse](t, added).CoverURL)
}
