package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
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
	ID           int64               `json:"id"`
	Title        string              `json:"title"`
	Description  string              `json:"description"`
	Category     string              `json:"category"`
	CategoryID   *int64              `json:"category_id"`
	CategorySlug string              `json:"category_slug"`
	File         libraryFileResponse `json:"file"`
	Published    bool                `json:"published"`
	PublishedAt  *time.Time          `json:"published_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
	// when the members were told by a push; null before
	NotifiedAt *time.Time `json:"notified_at"`
}

func documentCategoryIDPtr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	number := value.Int64
	return &number
}

func documentKind(contentType string) string {
	if contentType == media.PDF {
		return "pdf"
	}
	return "word"
}

func toLibraryDocument(row db.DocumentDetail) libraryDocumentResponse {
	category := row.Category
	if row.CategoryName.Valid {
		category = row.CategoryName.String
	}
	response := libraryDocumentResponse{
		ID:           row.ID,
		Title:        row.Title,
		Description:  row.Description,
		Category:     category,
		CategoryID:   documentCategoryIDPtr(row.CategoryID),
		CategorySlug: row.CategorySlug.String,
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
	// the same, for the apps that ask q
	Q string `form:"q"`
	// one category: its id, its slug ("prieres") or its name
	Category   string `form:"category"`
	CategoryID int64  `form:"category_id"`
}

func (s *Server) documentCategoryFilter(c *gin.Context, request documentsRequest) (int64, error) {
	if request.CategoryID != 0 {
		return request.CategoryID, nil
	}
	value := strings.TrimSpace(request.Category)
	if value == "" {
		return 0, nil
	}
	if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 {
		return id, nil
	}
	category, err := s.store.GetDocumentCategoryBySlug(c, value)
	if err == nil {
		return category.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	category, err = s.store.GetDocumentCategoryByName(c, value)
	if err == nil {
		return category.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	categories, err := s.store.ListDocumentCategories(c, db.ListDocumentCategoriesParams{WithDrafts: true, WithArchived: true})
	if err != nil {
		return 0, err
	}
	wanted := fold(value)
	for _, one := range categories {
		if fold(one.Name) == wanted || one.Slug == value {
			return one.ID, nil
		}
	}
	return 0, nil
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
	search := req.Q
	if strings.TrimSpace(search) == "" {
		search = req.Search
	}
	categoryID, err := s.documentCategoryFilter(c, req)
	if err != nil {
		internalServerError(c, err)
		return
	}
	limit, offset := req.limits()
	if req.Category != "" && categoryID == 0 {
		paginatedApiResponse(c, http.StatusOK, []libraryDocumentResponse{}, MetaResponse{
			Total: 0, Limit: limit, Offset: offset,
		})
		return
	}
	params := db.CountDocumentsFilteredParams{
		WithDrafts: withDrafts,
		CategoryID: categoryID,
		Search:     fold(search),
	}
	total, err := s.store.CountDocumentsFiltered(c, params)
	if err != nil {
		internalServerError(c, err)
		return
	}
	rows, err := s.store.ListDocumentsFiltered(c, db.ListDocumentsFilteredParams{
		WithDrafts: params.WithDrafts,
		CategoryID: params.CategoryID,
		Search:     params.Search,
		Lim:        limit,
		Off:        offset,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	documents := make([]libraryDocumentResponse, 0, len(rows))
	for _, row := range rows {
		documents = append(documents, toLibraryDocument(row))
	}
	paginatedApiResponse(c, http.StatusOK, documents, MetaResponse{
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

type libraryCategoryResponse struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Count int    `json:"count"`
}

// GET /v1/documents/categories: the categories of the published documents,
// with how many each, in the order chosen by the admins (the filter chips).
func (s *Server) listDocumentCategories(c *gin.Context) {
	rows, err := s.store.ListDocumentCategoryCounts(c, false)
	if err != nil {
		internalServerError(c, err)
		return
	}
	categories := make([]libraryCategoryResponse, 0, len(rows))
	for _, row := range rows {
		categories = append(categories, libraryCategoryResponse{
			ID: row.ID, Name: row.Name, Slug: row.Slug, Count: int(row.Count),
		})
	}
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
	// the category's name; an old app sends only this
	Category string `json:"category"`
	// the category chosen in the new form
	CategoryID *int64 `json:"category_id"`
	FileID     int64  `json:"file_id"`
	FileName   string `json:"file_name"`
	Published  bool   `json:"published"`
	// « Prévenir les membres » the first time it is published (true when
	// absent)
	Notify *bool `json:"notify"`
}

type checkedDocumentValues struct {
	CategoryID   sql.NullInt64
	CategoryName string
}

// checkDocument: trimmed, the lengths, the category found or created, and
// the file a PDF or a Word file.
func (s *Server) checkDocument(c *gin.Context, req *saveDocumentRequest) (checkedDocumentValues, error) {
	var values checkedDocumentValues
	req.Title = strings.TrimSpace(req.Title)
	req.Description = strings.TrimSpace(req.Description)
	req.Category = strings.Join(strings.Fields(req.Category), " ")
	req.FileName = strings.TrimSpace(filepath.Base(req.FileName))
	switch {
	case req.Title == "":
		return values, errors.New("title is required")
	case utf8.RuneCountInString(req.Title) > 200:
		return values, errors.New("title: 200 characters at most")
	case utf8.RuneCountInString(req.Description) > 2000:
		return values, errors.New("description: 2000 characters at most")
	case utf8.RuneCountInString(req.Category) > 120:
		return values, errors.New("category: 120 characters at most")
	case utf8.RuneCountInString(req.FileName) > 200:
		return values, errors.New("file_name: 200 characters at most")
	case req.FileID == 0:
		return values, errors.New("file_id is required")
	}
	var category db.DocumentCategory
	if req.CategoryID != nil && *req.CategoryID != 0 {
		row, err := s.store.GetDocumentCategory(c, *req.CategoryID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return values, errors.New("category_id: unknown category")
			}
			return values, err
		}
		category = row
	} else if req.Category != "" {
		row, err := s.store.GetDocumentCategoryByName(c, req.Category)
		if errors.Is(err, sql.ErrNoRows) {
			row, err = s.createDocumentCategoryFromName(c, req.Category)
		}
		if err != nil {
			return values, err
		}
		category = row
	}
	if category.ID != 0 {
		values.CategoryID = sql.NullInt64{Int64: category.ID, Valid: true}
		req.Category = category.Name
	}
	rows, err := s.store.ListMediaByIDs(c, []int64{req.FileID})
	if err != nil {
		return values, err
	}
	if len(rows) != 1 || !(media.Info{ContentType: rows[0].ContentType}).IsDocument() {
		return values, fmt.Errorf("file_id %d is not a PDF nor a Word file", req.FileID)
	}
	if req.FileName == "." || req.FileName == "" {
		req.FileName = req.Title + filepath.Ext(rows[0].Key)
	}
	return values, nil
}

func (s *Server) documentResponse(c *gin.Context, status int, id int64) {
	row, err := s.store.GetDocument(c, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "document")
			return
		}
		internalServerError(c, err)
		return
	}
	apiResponse(c, status, toLibraryDocument(row))
}

func (s *Server) documentByID(c *gin.Context, id int64) {
	s.documentResponse(c, http.StatusOK, id)
}

// POST /v1/admin/documents
func (s *Server) createDocument(c *gin.Context) {
	var req saveDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	values, err := s.checkDocument(c, &req)
	if err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	id, err := s.store.CreateDocument(c, db.CreateDocumentParams{
		Title:       req.Title,
		Description: req.Description,
		Category:    req.Category,
		CategoryID:  values.CategoryID,
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
	s.documentResponse(c, http.StatusCreated, id)
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
	values, err := s.checkDocument(c, &req)
	if err != nil {
		badRequestError(c, err)
		return
	}
	if _, err := s.store.UpdateDocument(c, db.UpdateDocumentParams{
		ID:          uri.ID,
		Title:       req.Title,
		Description: req.Description,
		Category:    req.Category,
		CategoryID:  values.CategoryID,
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
	// documentByID answers 200
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
