package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/hymns"
	"github.com/gin-gonic/gin"
)

type hymnBookResponse struct {
	Code      string `json:"code"`
	Title     string `json:"title"`
	Language  string `json:"language"`
	HymnCount int64  `json:"hymn_count"`
	// Version changes each time the book or one of its hymns changes:
	// the app downloads the book again only when it differs from its copy.
	Version string `json:"version"`
}

type hymnResponse struct {
	Number    int32        `json:"number"`
	Title     string       `json:"title"`
	Author    string       `json:"author,omitempty"`
	FirstLine string       `json:"first_line"`
	Parts     []hymns.Part `json:"parts"`
}

type hymnBookContentResponse struct {
	hymnBookResponse
	Hymns []hymnResponse `json:"hymns"`
}

// listHymnBooks returns the books, without their hymns: enough to know which ones changed.
func (s *Server) listHymnBooks(c *gin.Context) {
	books, err := s.store.ListHymnBooks(c)
	if err != nil {
		internalServerError(c)
		return
	}

	response := make([]hymnBookResponse, 0, len(books))
	for _, book := range books {
		response = append(response, hymnBookResponse{
			Code:      book.Code,
			Title:     book.Title,
			Language:  book.Language,
			HymnCount: book.HymnCount,
			Version:   hymnBookVersion(book.UpdatedAt),
		})
	}

	apiResponse(c, http.StatusOK, response)
}

type hymnBookURI struct {
	Code string `uri:"code" binding:"required,max=50"`
}

// getHymnBook returns a whole book, for the app's offline copy. It answers
// 304 Not Modified when the If-None-Match header holds the current ETag.
func (s *Server) getHymnBook(c *gin.Context) {
	var uri hymnBookURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}

	book, err := s.store.GetHymnBookByCode(c, uri.Code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Hymn book")
			return
		}
		internalServerError(c)
		return
	}

	version := hymnBookVersion(book.UpdatedAt)
	etag := fmt.Sprintf(`"%s@%s"`, book.Code, version)
	c.Header("ETag", etag)
	// the app may keep the book, but must ask whether it changed before using it
	c.Header("Cache-Control", "no-cache")
	if etagMatches(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}

	rows, err := s.store.ListBookHymns(c, book.ID)
	if err != nil {
		internalServerError(c)
		return
	}

	content := make([]hymnResponse, 0, len(rows))
	for _, row := range rows {
		hymn, err := newHymnResponse(row.Number, row.Title, row.Author, row.Parts)
		if err != nil {
			internalServerError(c)
			return
		}
		content = append(content, hymn)
	}

	apiResponse(c, http.StatusOK, hymnBookContentResponse{
		hymnBookResponse: hymnBookResponse{
			Code:      book.Code,
			Title:     book.Title,
			Language:  book.Language,
			HymnCount: int64(len(content)),
			Version:   version,
		},
		Hymns: content,
	})
}

type hymnURI struct {
	Code   string `uri:"code" binding:"required,max=50"`
	Number int32  `uri:"number" binding:"required,min=1"`
}

// getHymn returns one hymn, e.g. to open a shared link without the whole book.
func (s *Server) getHymn(c *gin.Context) {
	var uri hymnURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}

	book, err := s.store.GetHymnBookByCode(c, uri.Code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Hymn book")
			return
		}
		internalServerError(c)
		return
	}

	row, err := s.store.GetHymn(c, db.GetHymnParams{BookID: book.ID, Number: uri.Number})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Hymn")
			return
		}
		internalServerError(c)
		return
	}

	hymn, err := newHymnResponse(row.Number, row.Title, row.Author, row.Parts)
	if err != nil {
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusOK, hymn)
}

func newHymnResponse(number int32, title, author string, rawParts json.RawMessage) (hymnResponse, error) {
	var parts []hymns.Part
	if err := json.Unmarshal(rawParts, &parts); err != nil {
		return hymnResponse{}, fmt.Errorf("hymn %d: %w", number, err)
	}
	return hymnResponse{
		Number:    number,
		Title:     title,
		Author:    author,
		FirstLine: hymns.FirstLine(parts),
		Parts:     parts,
	}, nil
}

// hymnBookVersion turns the book's last change into a short, URL-safe version: "20261008T213005.123456Z".
func hymnBookVersion(updatedAt time.Time) string {
	return updatedAt.UTC().Format("20060102T150405.000000Z")
}

// etagMatches reads an If-None-Match header: one or several ETags, possibly weak (W/"..."), or *.
func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}
