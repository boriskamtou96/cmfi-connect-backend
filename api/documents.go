package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/media"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// Nos documents: the library of the community. The admins send a PDF or a
// Word file, give it a title, a category and publish it; every member finds
// the published ones, the newest first, a page at a time, by category or by
// a search on their words.

type libraryFileResponse struct {
	ID  int64  `json:"id"`
	URL string `json:"url"`
	// "Règlement intérieur.pdf": the name to save it under
	Name string `json:"name"`
	// pdf or word
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
}

type libraryDocumentResponse struct {
	ID          int64               `json:"id"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Category    string              `json:"category"`
	File        libraryFileResponse `json:"file"`
	Published   bool                `json:"published"`
	PublishedAt *time.Time          `json:"published_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
	// when the members were told by a push; null before
	NotifiedAt *time.Time `json:"notified_at"`
}

func documentKind(contentType string) string {
	if contentType == media.PDF {
		return "pdf"
	}
	return "word"
}

func toLibraryDocument(row db.ListDocumentsRow) libraryDocumentResponse {
	response := libraryDocumentResponse{
		ID:          row.ID,
		Title:       row.Title,
		Description: row.Description,
		Category:    row.Category,
		File: libraryFileResponse{
			ID:        row.FileID,
			URL:       mediaURL(row.FileKey),
			Name:      row.FileName,
			Kind:      documentKind(row.FileType),
			SizeBytes: row.FileSize,
		},
		Published: row.Published,
		UpdatedAt: row.UpdatedAt,
	}
	if row.PublishedAt.Valid {
		at := row.PublishedAt.Time
		response.PublishedAt = &at
	}
	response.NotifiedAt = timePtr(row.NotifiedAt)
	return response
}

type documentsRequest struct {
	pageRequest
	// words of the title, the description, the category or the file name;
	// case and accents left aside
	Search string `form:"search"`
	// one category; empty for all
	Category string `form:"category"`
}

// keeps tells whether a document answers the category and the search.
func (r documentsRequest) keeps(d libraryDocumentResponse) bool {
	if category := fold(r.Category); category != "" && fold(d.Category) != category {
		return false
	}
	words := strings.Fields(fold(r.Search))
	if len(words) == 0 {
		return true
	}
	text := fold(strings.Join([]string{d.Title, d.Description, d.Category, d.File.Name}, " "))
	for _, word := range words {
		if !strings.Contains(text, word) {
			return false
		}
	}
	return true
}

// GET /v1/documents?search=&category=&page=&size=: the published documents,
// the newest first.
func (s *Server) listDocuments(c *gin.Context) {
	s.documents(c, false)
}

// GET /v1/admin/documents?search=&category=&page=&size=: the drafts too
// (after the published ones).
func (s *Server) adminListDocuments(c *gin.Context) {
	s.documents(c, true)
}

func (s *Server) documents(c *gin.Context, withDrafts bool) {
	var req documentsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		badRequestError(c, err)
		return
	}
	rows, err := s.store.ListDocuments(c, withDrafts)
	if err != nil {
		internalServerError(c, err)
		return
	}
	kept := []libraryDocumentResponse{}
	for _, row := range rows {
		if document := toLibraryDocument(row); req.keeps(document) {
			kept = append(kept, document)
		}
	}
	limit, offset := req.limits()
	page := kept[min(int(offset), len(kept)):min(int(offset+limit), len(kept))]
	paginatedApiResponse(c, http.StatusOK, page, MetaResponse{
		Total:  int64(len(kept)),
		Limit:  limit,
		Offset: offset,
	})
}

type libraryCategoryResponse struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// GET /v1/documents/categories: the categories of the published documents,
// with how many each, in alphabetical order (the filter chips of the app).
func (s *Server) listDocumentCategories(c *gin.Context) {
	rows, err := s.store.ListDocuments(c, false)
	if err != nil {
		internalServerError(c, err)
		return
	}
	counts := map[string]*libraryCategoryResponse{}
	for _, row := range rows {
		name := strings.TrimSpace(row.Category)
		if name == "" {
			continue
		}
		// "Formulaires" and "formulaires" are one
		key := fold(name)
		if counts[key] == nil {
			counts[key] = &libraryCategoryResponse{Name: name}
		}
		counts[key].Count++
	}
	categories := make([]libraryCategoryResponse, 0, len(counts))
	for _, category := range counts {
		categories = append(categories, *category)
	}
	sort.Slice(categories, func(i, j int) bool {
		return fold(categories[i].Name) < fold(categories[j].Name)
	})
	apiResponse(c, http.StatusOK, categories)
}

type libraryUploadResponse struct {
	ID        int64  `json:"id"`
	URL       string `json:"url"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
}

// POST /v1/admin/documents/files (multipart form, field "file"): a PDF or a
// Word file of 10 MB at most, before the document that lists it. The usual
// POST /v1/admin/media still takes images and PDF only.
func (s *Server) uploadDocumentFile(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, media.MaxDocumentSize+1<<20)
	header, err := c.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			mediaTooLargeError(c)
			return
		}
		badRequestError(c, err)
		return
	}
	if header.Size > media.MaxDocumentSize {
		mediaTooLargeError(c)
		return
	}
	file, err := header.Open()
	if err != nil {
		badRequestError(c, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, media.MaxDocumentSize+1))
	if err != nil {
		badRequestError(c, err)
		return
	}
	info, err := media.InspectDocument(data, header.Filename)
	if err != nil {
		c.JSON(http.StatusUnsupportedMediaType, NewErrorResponse(http.StatusUnsupportedMediaType, "MEDIA_UNSUPPORTED", err))
		return
	}
	key := media.NewKey(info.Ext)
	if err := s.media.Save(key, data); err != nil {
		internalServerError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	row, err := s.store.CreateMedia(c, db.CreateMediaParams{
		Key:         key,
		ContentType: info.ContentType,
		SizeBytes:   int64(len(data)),
		UploadedBy:  nullID(authUser.ID),
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusCreated, libraryUploadResponse{
		ID:        row.ID,
		URL:       mediaURL(row.Key),
		Name:      filepath.Base(header.Filename),
		Kind:      documentKind(row.ContentType),
		SizeBytes: row.SizeBytes,
	})
}

type saveDocumentRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"`
	FileID      int64  `json:"file_id"`
	FileName    string `json:"file_name"`
	Published   bool   `json:"published"`
	// « Prévenir les membres » the first time it is published (true when
	// absent)
	Notify *bool `json:"notify"`
}

// checked: trimmed, the lengths, and the file a PDF or a Word file.
func (s *Server) checkDocument(c *gin.Context, req *saveDocumentRequest) error {
	req.Title = strings.TrimSpace(req.Title)
	req.Description = strings.TrimSpace(req.Description)
	req.Category = strings.Join(strings.Fields(req.Category), " ")
	req.FileName = strings.TrimSpace(filepath.Base(req.FileName))
	switch {
	case req.Title == "":
		return errors.New("title is required")
	case utf8.RuneCountInString(req.Title) > 200:
		return errors.New("title: 200 characters at most")
	case utf8.RuneCountInString(req.Description) > 2000:
		return errors.New("description: 2000 characters at most")
	case utf8.RuneCountInString(req.Category) > 60:
		return errors.New("category: 60 characters at most")
	case utf8.RuneCountInString(req.FileName) > 200:
		return errors.New("file_name: 200 characters at most")
	case req.FileID == 0:
		return errors.New("file_id is required")
	}
	rows, err := s.store.ListMediaByIDs(c, []int64{req.FileID})
	if err != nil {
		return err
	}
	if len(rows) != 1 || !(media.Info{ContentType: rows[0].ContentType}).IsDocument() {
		return fmt.Errorf("file_id %d is not a PDF nor a Word file", req.FileID)
	}
	if req.FileName == "." || req.FileName == "" {
		req.FileName = req.Title + filepath.Ext(rows[0].Key)
	}
	return nil
}

func (s *Server) documentByID(c *gin.Context, id int64) {
	row, err := s.store.GetDocument(c, id)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, toLibraryDocument(db.ListDocumentsRow(row)))
}

// POST /v1/admin/documents
func (s *Server) createDocument(c *gin.Context) {
	var req saveDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if err := s.checkDocument(c, &req); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	id, err := s.store.CreateDocument(c, db.CreateDocumentParams{
		Title:       req.Title,
		Description: req.Description,
		Category:    req.Category,
		FileID:      req.FileID,
		FileName:    req.FileName,
		Published:   req.Published,
		CreatedBy:   nullID(authUser.ID),
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	s.announceDocument(c, id, req.Notify)
	row, err := s.store.GetDocument(c, id)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusCreated, toLibraryDocument(db.ListDocumentsRow(row)))
}

type documentURI struct {
	ID int64 `uri:"id" binding:"required,min=1"`
}

// PUT /v1/admin/documents/:id: everything at once; a file replaced is
// deleted.
func (s *Server) updateDocument(c *gin.Context) {
	var uri documentURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req saveDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	before, err := s.store.GetDocument(c, uri.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "document")
			return
		}
		internalServerError(c, err)
		return
	}
	if err := s.checkDocument(c, &req); err != nil {
		badRequestError(c, err)
		return
	}
	if _, err := s.store.UpdateDocument(c, db.UpdateDocumentParams{
		ID:          uri.ID,
		Title:       req.Title,
		Description: req.Description,
		Category:    req.Category,
		FileID:      req.FileID,
		FileName:    req.FileName,
		Published:   req.Published,
	}); err != nil {
		internalServerError(c, err)
		return
	}
	if before.FileID != req.FileID {
		s.dropDocumentFile(c, before.FileID)
	}
	s.announceDocument(c, uri.ID, req.Notify)
	s.documentByID(c, uri.ID)
}

// DELETE /v1/admin/documents/:id: the document and its file.
func (s *Server) deleteDocument(c *gin.Context) {
	var uri documentURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	fileID, err := s.store.DeleteDocument(c, uri.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "document")
			return
		}
		internalServerError(c, err)
		return
	}
	s.dropDocumentFile(c, fileID)
	c.Status(http.StatusNoContent)
}

// dropDocumentFile deletes a file no document lists any more.
func (s *Server) dropDocumentFile(c *gin.Context, fileID int64) {
	count, err := s.store.CountDocumentsWithFile(c, fileID)
	if err != nil {
		logFor(c).Error("cannot count the documents of a file", "media_id", fileID, "error", err)
		return
	}
	if count == 0 {
		s.dropMedia(c, fileID)
	}
}
