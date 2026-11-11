package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// Mes lectures: the books a member reads (Lecture de la littérature
// chrétienne). The LLC entry of a report names the book read that day; its
// pages add up to the book's progress. Finished books count per month.

const (
	bookReading   = "reading"
	bookFinished  = "finished"
	bookAbandoned = "abandoned"
	// covers are only taken from the search's own server
	openLibraryCovers = "https://covers.openlibrary.org/"
)

// openLibrarySearchURL is where book searches go (a test server in tests).
var openLibrarySearchURL = "https://openlibrary.org/search.json"

var bookSearchClient = &http.Client{Timeout: 6 * time.Second}

type readingBookResponse struct {
	ID         int64   `json:"id"`
	Title      string  `json:"title"`
	Author     string  `json:"author"`
	TotalPages int32   `json:"total_pages"`
	CoverURL   string  `json:"cover_url"`
	Status     string  `json:"status"`
	StartedOn  string  `json:"started_on"`
	FinishedOn *string `json:"finished_on"`
	// the pages of the reports that name it (may pass total_pages)
	PagesRead int32 `json:"pages_read"`
}

func toReadingBook(row db.ListReadingBooksRow) readingBookResponse {
	book := readingBookResponse{
		ID:         row.ID,
		Title:      row.Title,
		Author:     row.Author,
		TotalPages: row.TotalPages,
		CoverURL:   row.CoverUrl,
		Status:     row.Status,
		StartedOn:  row.StartedOn.Format(reportDateLayout),
		PagesRead:  row.PagesRead,
	}
	if row.FinishedOn.Valid {
		finished := row.FinishedOn.Time.Format(reportDateLayout)
		book.FinishedOn = &finished
	}
	return book
}

// readingBook is one of the member's books, with its progress.
func (s *Server) readingBook(c *gin.Context, userID, id int64) (readingBookResponse, bool, error) {
	rows, err := s.store.ListReadingBooks(c, userID)
	if err != nil {
		return readingBookResponse{}, false, err
	}
	for _, row := range rows {
		if row.ID == id {
			return toReadingBook(row), true, nil
		}
	}
	return readingBookResponse{}, false, nil
}

// GET /v1/reading/books: the member's books, those being read first.
func (s *Server) listReadingBooks(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	rows, err := s.store.ListReadingBooks(c, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	books := make([]readingBookResponse, 0, len(rows))
	for _, row := range rows {
		books = append(books, toReadingBook(row))
	}
	apiResponse(c, http.StatusOK, books)
}

type readingBookRequest struct {
	Title      string `json:"title" binding:"required,max=300"`
	Author     string `json:"author" binding:"max=200"`
	TotalPages int32  `json:"total_pages" binding:"required,min=1,max=20000"`
	CoverURL   string `json:"cover_url" binding:"max=500"`
}

func (r *readingBookRequest) clean() error {
	r.Title = strings.TrimSpace(r.Title)
	r.Author = strings.TrimSpace(r.Author)
	r.CoverURL = strings.TrimSpace(r.CoverURL)
	if r.Title == "" {
		return errors.New("title is required")
	}
	if r.CoverURL != "" && !strings.HasPrefix(r.CoverURL, openLibraryCovers) {
		return fmt.Errorf("cover_url must come from %s", openLibraryCovers)
	}
	return nil
}

// POST /v1/reading/books: a book added to the shelf, being read. 201 with it.
func (s *Server) createReadingBook(c *gin.Context) {
	var req readingBookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if err := req.clean(); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	id, err := s.store.CreateReadingBook(c, db.CreateReadingBookParams{
		UserID:     authUser.ID,
		Title:      req.Title,
		Author:     req.Author,
		TotalPages: req.TotalPages,
		CoverUrl:   req.CoverURL,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	book, _, err := s.readingBook(c, authUser.ID, id)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusCreated, book)
}

type readingBookURI struct {
	ID int64 `uri:"id" binding:"required,min=1"`
}

// PUT /v1/reading/books/:id: its title, author, pages or cover corrected.
func (s *Server) updateReadingBook(c *gin.Context) {
	var uri readingBookURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req readingBookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if err := req.clean(); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	changed, err := s.store.UpdateReadingBook(c, db.UpdateReadingBookParams{
		ID:         uri.ID,
		UserID:     authUser.ID,
		Title:      req.Title,
		Author:     req.Author,
		TotalPages: req.TotalPages,
		CoverUrl:   req.CoverURL,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	if changed == 0 {
		resourceNotFoundError(c, "Book")
		return
	}
	s.sendReadingBook(c, authUser.ID, uri.ID, http.StatusOK)
}

type readingBookStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=reading finished abandoned"`
	// the day it was finished ("finished" only): today when absent
	FinishedOn string `json:"finished_on"`
}

// PUT /v1/reading/books/:id/status: finished (on a day, today by default),
// abandoned, or being read again.
func (s *Server) setReadingBookStatus(c *gin.Context) {
	var uri readingBookURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req readingBookStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	params := db.SetReadingBookStatusParams{ID: uri.ID, UserID: authUser.ID, Status: req.Status}
	if req.Status == bookFinished {
		day := today()
		if req.FinishedOn != "" {
			parsed, err := parseReportDate(req.FinishedOn)
			if err != nil {
				badRequestError(c, err)
				return
			}
			// the member's day may be ahead of the server's: one day of leeway
			if parsed.After(day.AddDate(0, 0, 1)) {
				badRequestError(c, errors.New("finished_on can't be in the future"))
				return
			}
			day = parsed
		}
		params.FinishedOn = sql.NullTime{Time: day, Valid: true}
	} else if req.FinishedOn != "" {
		badRequestError(c, errors.New("finished_on goes with the finished status only"))
		return
	}

	changed, err := s.store.SetReadingBookStatus(c, params)
	if err != nil {
		internalServerError(c, err)
		return
	}
	if changed == 0 {
		resourceNotFoundError(c, "Book")
		return
	}
	s.sendReadingBook(c, authUser.ID, uri.ID, http.StatusOK)
}

func (s *Server) sendReadingBook(c *gin.Context, userID, id int64, status int) {
	book, found, err := s.readingBook(c, userID, id)
	if err != nil {
		internalServerError(c, err)
		return
	}
	if !found {
		resourceNotFoundError(c, "Book")
		return
	}
	apiResponse(c, status, book)
}

// DELETE /v1/reading/books/:id: 204, twice too. The reports keep their pages,
// without the book.
func (s *Server) deleteReadingBook(c *gin.Context) {
	var uri readingBookURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if _, err := s.store.DeleteReadingBook(c, db.DeleteReadingBookParams{ID: uri.ID, UserID: authUser.ID}); err != nil {
		internalServerError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type readingStatsQuery struct {
	Year int `form:"year" binding:"omitempty,min=2000,max=2100"`
}

type readingMonth struct {
	// "2026-11"
	Month string `json:"month"`
	Books int32  `json:"books"`
}

type readingStatsResponse struct {
	Year int `json:"year"`
	// the 12 months of the year, January first
	Months        []readingMonth `json:"months"`
	BooksFinished int32          `json:"books_finished"`
	// the pages read in books that year
	Pages int32 `json:"pages"`
}

// GET /v1/reading/stats?year=: the books finished each month of a year (this
// year by default) and the pages read in books.
func (s *Server) getReadingStats(c *gin.Context) {
	var query readingStatsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		badRequestError(c, err)
		return
	}
	year := query.Year
	if year == 0 {
		year = today().Year()
	}
	from := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	rows, err := s.store.CountFinishedBooksByMonth(c, db.CountFinishedBooksByMonthParams{
		UserID:   authUser.ID,
		FromDate: from,
		ToDate:   to,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	pages, err := s.store.SumBookPages(c, db.SumBookPagesParams{
		UserID:   authUser.ID,
		FromDate: from,
		ToDate:   to,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}

	counts := make(map[time.Month]int32, len(rows))
	for _, row := range rows {
		counts[row.Month.Month()] = row.Books
	}
	stats := readingStatsResponse{Year: year, Months: make([]readingMonth, 0, 12), Pages: pages}
	for month := time.January; month <= time.December; month++ {
		books := counts[month]
		stats.Months = append(stats.Months, readingMonth{Month: fmt.Sprintf("%04d-%02d", year, int(month)), Books: books})
		stats.BooksFinished += books
	}
	apiResponse(c, http.StatusOK, stats)
}

type bookSearchQuery struct {
	Q string `form:"q" binding:"required,min=2,max=200"`
}

// bookFound is a book the search proposes: what the member would type.
// TotalPages is 0 when the search doesn't know it.
type bookFound struct {
	Title      string `json:"title"`
	Author     string `json:"author"`
	TotalPages int32  `json:"total_pages"`
	CoverURL   string `json:"cover_url"`
}

// GET /v1/reading/search?q=: books matching a title or an author, from Open
// Library (no key). 503 SEARCH_UNAVAILABLE when it can't be reached: the
// member types the book by hand.
func (s *Server) searchBooks(c *gin.Context) {
	var query bookSearchQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		badRequestError(c, err)
		return
	}
	books, err := searchOpenLibrary(c, strings.TrimSpace(query.Q))
	if err != nil {
		logFor(c).Warn("book search failed", "error", err)
		c.JSON(http.StatusServiceUnavailable, NewErrorResponse(http.StatusServiceUnavailable, "SEARCH_UNAVAILABLE", err))
		return
	}
	apiResponse(c, http.StatusOK, books)
}

func searchOpenLibrary(c *gin.Context, q string) ([]bookFound, error) {
	params := url.Values{}
	params.Set("q", q)
	params.Set("fields", "title,author_name,number_of_pages_median,cover_i")
	params.Set("limit", "12")
	request, err := http.NewRequestWithContext(c, http.MethodGet, openLibrarySearchURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "CMFI-Connect/1.0")
	response, err := bookSearchClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("open library answered %d", response.StatusCode)
	}

	var body struct {
		Docs []struct {
			Title      string   `json:"title"`
			AuthorName []string `json:"author_name"`
			Pages      int32    `json:"number_of_pages_median"`
			CoverID    int64    `json:"cover_i"`
		} `json:"docs"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&body); err != nil {
		return nil, err
	}

	books := make([]bookFound, 0, len(body.Docs))
	for _, doc := range body.Docs {
		title := strings.TrimSpace(doc.Title)
		if title == "" {
			continue
		}
		book := bookFound{Title: cut(title, 300), Author: cut(strings.Join(doc.AuthorName, ", "), 200)}
		if doc.Pages > 0 && doc.Pages <= 20000 {
			book.TotalPages = doc.Pages
		}
		if doc.CoverID > 0 {
			book.CoverURL = fmt.Sprintf("%sb/id/%d-M.jpg", openLibraryCovers, doc.CoverID)
		}
		books = append(books, book)
	}
	return books, nil
}

// cut keeps the first n letters of value.
func cut(value string, n int) string {
	runes := []rune(value)
	if len(runes) <= n {
		return value
	}
	return strings.TrimSpace(string(runes[:n]))
}
