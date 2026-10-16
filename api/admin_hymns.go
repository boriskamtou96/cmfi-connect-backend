package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/hymns"
	"github.com/gin-gonic/gin"
)

// The admins keep the hymn books from the back office: create a book, rename
// or move it, add, correct or remove a hymn. Each change gives the book a new
// version (updated_at): the apps download it again at their next check.

// the code of a book appears in the URLs: "christ-est-vainqueur"
var hymnBookCode = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type adminHymnBookResponse struct {
	hymnBookResponse
	Position int32 `json:"position"`
}

// GET /v1/admin/hymns/books: the books in their order, with their position.
func (s *Server) adminListHymnBooks(c *gin.Context) {
	books, err := s.store.ListHymnBooks(c)
	if err != nil {
		internalServerError(c, err)
		return
	}
	response := make([]adminHymnBookResponse, 0, len(books))
	for _, book := range books {
		response = append(response, adminHymnBookResponse{
			hymnBookResponse: hymnBookResponse{
				Code:      book.Code,
				Title:     book.Title,
				Language:  book.Language,
				HymnCount: book.HymnCount,
				Version:   hymnBookVersion(book.UpdatedAt),
			},
			Position: book.Position,
		})
	}
	apiResponse(c, http.StatusOK, response)
}

type hymnBookRequest struct {
	// Code is read on creation only: it never changes (links and copies use it).
	Code     string `json:"code"`
	Title    string `json:"title" binding:"required,max=200"`
	Language string `json:"language" binding:"required,max=10"`
	// Position orders the books in the app; on creation, after the others when
	// not given.
	Position *int32 `json:"position"`
}

func (r hymnBookRequest) trimmed() hymnBookRequest {
	r.Code = strings.TrimSpace(r.Code)
	r.Title = strings.TrimSpace(r.Title)
	r.Language = strings.ToLower(strings.TrimSpace(r.Language))
	return r
}

// POST /v1/admin/hymns/books: a new book, without hymns yet.
func (s *Server) createHymnBook(c *gin.Context) {
	var req hymnBookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	req = req.trimmed()
	if !hymnBookCode.MatchString(req.Code) || len(req.Code) > 50 {
		badRequestError(c, fmt.Errorf("code: lowercase letters, digits and dashes (christ-est-vainqueur)"))
		return
	}
	if req.Title == "" || req.Language == "" {
		badRequestError(c, fmt.Errorf("title and language are required"))
		return
	}

	_, err := s.store.GetHymnBookByCode(c, req.Code)
	if err == nil {
		conflictError(c, fmt.Errorf("a book with the code %s already exists", req.Code))
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		internalServerError(c, err)
		return
	}

	position := int32(0)
	if req.Position != nil {
		position = *req.Position
	} else {
		books, err := s.store.ListHymnBooks(c)
		if err != nil {
			internalServerError(c, err)
			return
		}
		for _, book := range books {
			position = max(position, book.Position+1)
		}
	}

	book, err := s.store.CreateHymnBook(c, db.CreateHymnBookParams{
		Code:     req.Code,
		Title:    req.Title,
		Language: req.Language,
		Position: position,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusCreated, newAdminHymnBookResponse(book, 0))
}

// PUT /v1/admin/hymns/books/:code: its title, language or position.
func (s *Server) updateHymnBook(c *gin.Context) {
	var uri hymnBookURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req hymnBookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	req = req.trimmed()
	if req.Title == "" || req.Language == "" {
		badRequestError(c, fmt.Errorf("title and language are required"))
		return
	}

	book, ok := s.hymnBookOf(c, uri.Code)
	if !ok {
		return
	}
	position := book.Position
	if req.Position != nil {
		position = *req.Position
	}
	changed, err := s.store.UpdateHymnBook(c, db.UpdateHymnBookParams{
		Title:    req.Title,
		Language: req.Language,
		Position: position,
		ID:       book.ID,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	if changed > 0 {
		if err := s.store.TouchHymnBook(c, book.ID); err != nil {
			internalServerError(c, err)
			return
		}
	}

	updated, ok := s.hymnBookOf(c, uri.Code)
	if !ok {
		return
	}
	count, err := s.store.ListHymnNumbers(c, updated.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, newAdminHymnBookResponse(updated, int64(len(count))))
}

type hymnRequest struct {
	Title  string       `json:"title" binding:"max=300"`
	Author string       `json:"author" binding:"max=500"`
	Parts  []hymns.Part `json:"parts"`
}

// PUT /v1/admin/hymns/books/:code/hymns/:number: adds the hymn, or replaces
// it. 201 when it is new, 200 otherwise.
func (s *Server) saveHymn(c *gin.Context) {
	var uri hymnURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req hymnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}

	book, ok := s.hymnBookOf(c, uri.Code)
	if !ok {
		return
	}

	// spaces around the words never count
	hymn := hymns.Hymn{
		Number: uri.Number,
		Title:  strings.TrimSpace(req.Title),
		Author: strings.TrimSpace(req.Author),
		Parts:  make([]hymns.Part, 0, len(req.Parts)),
	}
	for _, part := range req.Parts {
		lines := make([]string, 0, len(part.Lines))
		for _, line := range part.Lines {
			lines = append(lines, strings.TrimSpace(line))
		}
		part.Label = strings.TrimSpace(part.Label)
		part.Lines = lines
		hymn.Parts = append(hymn.Parts, part)
	}
	// the rules of the imported books: a title, parts with lines, no empty line
	check := hymns.File{
		Book:  hymns.Book{Code: book.Code, Title: book.Title, Language: book.Language},
		Hymns: []hymns.Hymn{hymn},
	}
	if err := check.Validate(); err != nil {
		badRequestError(c, err)
		return
	}

	_, err := s.store.GetHymn(c, db.GetHymnParams{BookID: book.ID, Number: uri.Number})
	created := errors.Is(err, sql.ErrNoRows)
	if err != nil && !created {
		internalServerError(c, err)
		return
	}

	parts, err := json.Marshal(hymn.Parts)
	if err != nil {
		internalServerError(c, err)
		return
	}
	changed, err := s.store.UpsertHymn(c, db.UpsertHymnParams{
		BookID: book.ID,
		Number: hymn.Number,
		Title:  hymn.Title,
		Author: hymn.Author,
		Parts:  parts,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	if changed > 0 {
		if err := s.store.TouchHymnBook(c, book.ID); err != nil {
			internalServerError(c, err)
			return
		}
	}

	response, err := newHymnResponse(hymn.Number, hymn.Title, hymn.Author, parts)
	if err != nil {
		internalServerError(c, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	apiResponse(c, status, response)
}

// DELETE /v1/admin/hymns/books/:code/hymns/:number
func (s *Server) deleteHymn(c *gin.Context) {
	var uri hymnURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	book, ok := s.hymnBookOf(c, uri.Code)
	if !ok {
		return
	}
	deleted, err := s.store.DeleteHymn(c, db.DeleteHymnParams{BookID: book.ID, Number: uri.Number})
	if err != nil {
		internalServerError(c, err)
		return
	}
	if deleted == 0 {
		resourceNotFoundError(c, "Hymn")
		return
	}
	if err := s.store.TouchHymnBook(c, book.ID); err != nil {
		internalServerError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// hymnBookOf reads the book of [code]; answers 404 or 500 itself.
func (s *Server) hymnBookOf(c *gin.Context, code string) (db.HymnBook, bool) {
	book, err := s.store.GetHymnBookByCode(c, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Hymn book")
		} else {
			internalServerError(c, err)
		}
		return db.HymnBook{}, false
	}
	return book, true
}

func newAdminHymnBookResponse(book db.HymnBook, count int64) adminHymnBookResponse {
	return adminHymnBookResponse{
		hymnBookResponse: hymnBookResponse{
			Code:      book.Code,
			Title:     book.Title,
			Language:  book.Language,
			HymnCount: count,
			Version:   hymnBookVersion(book.UpdatedAt),
		},
		Position: book.Position,
	}
}
