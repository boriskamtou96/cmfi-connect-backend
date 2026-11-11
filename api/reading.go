package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
	// covers are only taken from the servers of the searches
	openLibraryCovers = "https://covers.openlibrary.org/"
	googleBooksCovers = "https://books.google.com/books/content?"
	// the time a book typed by hand waits for its cover
	coverLookupTimeout = 5 * time.Second
)

// Where book searches go (test servers in tests): Open Library first, Google
// Books when it finds nothing (it knows more books in French).
var (
	openLibrarySearchURL = "https://openlibrary.org/search.json"
	googleBooksURL       = "https://www.googleapis.com/books/v1/volumes"
)

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
	if r.CoverURL != "" && !strings.HasPrefix(r.CoverURL, openLibraryCovers) && !strings.HasPrefix(r.CoverURL, googleBooksCovers) {
		return fmt.Errorf("cover_url must come from %s or %s", openLibraryCovers, googleBooksCovers)
	}
	return nil
}

// withCover looks for the cover of a book typed by hand (title and author),
// so the shelf shows the real book rather than a drawn one. Without one found
// in time, the book keeps none: the app draws it.
func (r *readingBookRequest) withCover(c *gin.Context) {
	if r.CoverURL != "" {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), coverLookupTimeout)
	defer cancel()
	cover, err := findBookCover(ctx, r.Title, r.Author)
	if err != nil {
		logFor(c).Info("book cover not found", "error", err)
	}
	r.CoverURL = cover
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
	req.withCover(c)
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
	req.withCover(c)
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
// Library, else Google Books (no key for either). 503 SEARCH_UNAVAILABLE when
// neither can be reached: the member types the book by hand.
func (s *Server) searchBooks(c *gin.Context) {
	var query bookSearchQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		badRequestError(c, err)
		return
	}
	q := strings.TrimSpace(query.Q)
	params := url.Values{}
	params.Set("q", q)
	books, err := searchOpenLibrary(c, params, 12)
	if err != nil || len(books) == 0 {
		if more, googleErr := searchGoogleBooks(c, q, 12); googleErr == nil {
			books, err = more, nil
		} else if err == nil {
			logFor(c).Info("google books search failed", "error", googleErr)
		}
	}
	if err != nil {
		logFor(c).Warn("book search failed", "error", err)
		c.JSON(http.StatusServiceUnavailable, NewErrorResponse(http.StatusServiceUnavailable, "SEARCH_UNAVAILABLE", err))
		return
	}
	apiResponse(c, http.StatusOK, books)
}

// findBookCover is the cover of the first book found with this title and
// author: Open Library, then Google Books. "" when neither has one.
func findBookCover(ctx context.Context, title, author string) (string, error) {
	params := url.Values{}
	params.Set("title", title)
	if author != "" {
		params.Set("author", author)
	}
	found, libraryErr := searchOpenLibrary(ctx, params, 5)
	for _, book := range found {
		if book.CoverURL != "" {
			return book.CoverURL, nil
		}
	}

	q := fmt.Sprintf("intitle:%q", title)
	if author != "" {
		q += fmt.Sprintf(" inauthor:%q", author)
	}
	found, googleErr := searchGoogleBooks(ctx, q, 5)
	for _, book := range found {
		if book.CoverURL != "" {
			return book.CoverURL, nil
		}
	}
	return "", errors.Join(libraryErr, googleErr)
}

// getBookSearch asks a search server for its JSON answer, 2 MB at most.
func getBookSearch(ctx context.Context, address string, params url.Values, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "CMFI-Connect/1.0")
	response, err := bookSearchClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", request.URL.Host, response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(into)
}

// searchOpenLibrary: params "q", or "title" and "author".
func searchOpenLibrary(ctx context.Context, params url.Values, limit int) ([]bookFound, error) {
	params.Set("fields", "title,author_name,number_of_pages_median,cover_i")
	params.Set("limit", strconv.Itoa(limit))

	var body struct {
		Docs []struct {
			Title      string   `json:"title"`
			AuthorName []string `json:"author_name"`
			Pages      int32    `json:"number_of_pages_median"`
			CoverID    int64    `json:"cover_i"`
		} `json:"docs"`
	}
	if err := getBookSearch(ctx, openLibrarySearchURL, params, &body); err != nil {
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

// searchGoogleBooks: q as Google Books reads it (intitle:, inauthor:).
func searchGoogleBooks(ctx context.Context, q string, limit int) ([]bookFound, error) {
	params := url.Values{}
	params.Set("q", q)
	params.Set("maxResults", strconv.Itoa(limit))
	params.Set("printType", "books")
	params.Set("fields", "items(volumeInfo(title,authors,pageCount,imageLinks/thumbnail))")

	var body struct {
		Items []struct {
			VolumeInfo struct {
				Title      string   `json:"title"`
				Authors    []string `json:"authors"`
				PageCount  int32    `json:"pageCount"`
				ImageLinks struct {
					Thumbnail string `json:"thumbnail"`
				} `json:"imageLinks"`
			} `json:"volumeInfo"`
		} `json:"items"`
	}
	if err := getBookSearch(ctx, googleBooksURL, params, &body); err != nil {
		return nil, err
	}

	books := make([]bookFound, 0, len(body.Items))
	for _, item := range body.Items {
		info := item.VolumeInfo
		title := strings.TrimSpace(info.Title)
		if title == "" {
			continue
		}
		book := bookFound{
			Title:    cut(title, 300),
			Author:   cut(strings.Join(info.Authors, ", "), 200),
			CoverURL: googleBooksCover(info.ImageLinks.Thumbnail),
		}
		if info.PageCount > 0 && info.PageCount <= 20000 {
			book.TotalPages = info.PageCount
		}
		books = append(books, book)
	}
	return books, nil
}

// googleBooksCover keeps only the book's id from a Google Books thumbnail
// ("http://books.google.com/books/content?id=X&printsec=frontcover&img=1
// &zoom=1&edge=curl&source=gbs_api"): https, without the curled corner.
func googleBooksCover(thumbnail string) string {
	link, err := url.Parse(thumbnail)
	if err != nil || link.Host != "books.google.com" || link.Path != "/books/content" {
		return ""
	}
	id := link.Query().Get("id")
	if id == "" {
		return ""
	}
	return googleBooksCovers + "id=" + url.QueryEscape(id) + "&printsec=frontcover&img=1&zoom=1"
}

// cut keeps the first n letters of value.
func cut(value string, n int) string {
	runes := []rune(value)
	if len(runes) <= n {
		return value
	}
	return strings.TrimSpace(string(runes[:n]))
}
