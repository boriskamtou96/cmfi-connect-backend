package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

// The categories of Nos documents: Prières, Traités, Prophéties, Réveil…
// The admins create, rename and archive them; an archived category keeps its
// documents but leaves the forms.

var documentNonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func documentSlugify(value string) string {
	return strings.Trim(documentNonSlug.ReplaceAllString(fold(value), "-"), "-")
}

func documentNullIDPtr(id *int64) sql.NullInt64 {
	if id == nil || *id == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *id, Valid: true}
}

type adminDocumentCategoryResponse struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Slug          string    `json:"slug"`
	Description   string    `json:"description"`
	Color         string    `json:"color"`
	Icon          string    `json:"icon"`
	ParentID      *int64    `json:"parent_id"`
	Position      int32     `json:"position"`
	Archived      bool      `json:"archived"`
	DocumentCount int32     `json:"document_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func toAdminDocumentCategory(row db.ListDocumentCategoriesRow) adminDocumentCategoryResponse {
	return adminDocumentCategoryResponse{
		ID:            row.ID,
		Name:          row.Name,
		Slug:          row.Slug,
		Description:   row.Description,
		Color:         row.Color,
		Icon:          row.Icon,
		ParentID:      documentCategoryIDPtr(row.ParentID),
		Position:      row.Position,
		Archived:      row.ArchivedAt.Valid,
		DocumentCount: row.DocumentCount,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}

func documentCategoryFromSaved(row db.DocumentCategory) adminDocumentCategoryResponse {
	return adminDocumentCategoryResponse{
		ID:          row.ID,
		Name:        row.Name,
		Slug:        row.Slug,
		Description: row.Description,
		Color:       row.Color,
		Icon:        row.Icon,
		ParentID:    documentCategoryIDPtr(row.ParentID),
		Position:    row.Position,
		Archived:    row.ArchivedAt.Valid,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

// GET /v1/admin/document-categories: every category, archived ones too, with
// how many documents each (drafts included).
func (s *Server) adminListDocumentCategories(c *gin.Context) {
	rows, err := s.store.ListDocumentCategories(c, db.ListDocumentCategoriesParams{
		WithDrafts:   true,
		WithArchived: true,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	categories := make([]adminDocumentCategoryResponse, 0, len(rows))
	for _, row := range rows {
		categories = append(categories, toAdminDocumentCategory(row))
	}
	apiResponse(c, http.StatusOK, categories)
}

type saveDocumentCategoryRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Color       string `json:"color"`
	Icon        string `json:"icon"`
	ParentID    *int64 `json:"parent_id"`
	Position    int32  `json:"position"`
}

// checkedDocumentCategory: the name and the lengths checked; the slug is the
// one given, else the one it already had, else the name's. A slug stays the
// same when a category is renamed: the links of the apps keep working.
func (s *Server) checkedDocumentCategory(c *gin.Context, req *saveDocumentCategoryRequest, id int64, currentSlug string) (slug string, err error) {
	req.Name = strings.Join(strings.Fields(req.Name), " ")
	req.Description = strings.TrimSpace(req.Description)
	req.Color = strings.TrimSpace(req.Color)
	req.Icon = strings.TrimSpace(req.Icon)
	switch {
	case req.Name == "":
		return "", errors.New("name is required")
	case utf8.RuneCountInString(req.Name) > 80:
		return "", errors.New("name: 80 characters at most")
	case utf8.RuneCountInString(req.Description) > 500:
		return "", errors.New("description: 500 characters at most")
	case utf8.RuneCountInString(req.Color) > 20:
		return "", errors.New("color: 20 characters at most")
	case utf8.RuneCountInString(req.Icon) > 40:
		return "", errors.New("icon: 40 characters at most")
	case req.Position < 0 || req.Position > 10000:
		return "", errors.New("position: 0 to 10000")
	}
	slug = documentSlugify(req.Slug)
	if slug == "" {
		slug = currentSlug
	}
	if slug == "" {
		slug = documentSlugify(req.Name)
	}
	if slug == "" {
		slug = "categorie"
	}
	if req.ParentID != nil && *req.ParentID != 0 {
		if *req.ParentID == id {
			return "", errors.New("parent_id: a category cannot contain itself")
		}
		parent, err := s.store.GetDocumentCategory(c, *req.ParentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", errors.New("parent_id: unknown category")
			}
			return "", err
		}
		if parent.ArchivedAt.Valid {
			return "", errors.New("parent_id: this category is archived")
		}
	} else {
		req.ParentID = nil
	}
	byName, err := s.store.GetDocumentCategoryByName(c, req.Name)
	if err == nil && byName.ID != id {
		return "", errors.New("a category with this name already exists")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	bySlug, err := s.store.GetDocumentCategoryBySlugAny(c, slug)
	if err == nil && bySlug.ID != id {
		return "", errors.New("a category with this slug already exists")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return slug, nil
}

func documentConflictFromDatabase(c *gin.Context, err error, message string) bool {
	var databaseError *pq.Error
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		conflictError(c, errors.New(message))
		return true
	}
	return false
}

// POST /v1/admin/document-categories
func (s *Server) createDocumentCategory(c *gin.Context) {
	var req saveDocumentCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	slug, err := s.checkedDocumentCategory(c, &req, 0, "")
	if err != nil {
		badRequestError(c, err)
		return
	}
	row, err := s.store.CreateDocumentCategory(c, db.CreateDocumentCategoryParams{
		Name:        req.Name,
		Slug:        slug,
		Description: req.Description,
		Color:       req.Color,
		Icon:        req.Icon,
		ParentID:    documentNullIDPtr(req.ParentID),
		Position:    req.Position,
	})
	if err != nil {
		if documentConflictFromDatabase(c, err, "this category already exists") {
			return
		}
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusCreated, documentCategoryFromSaved(row))
}

// PUT /v1/admin/document-categories/:id
func (s *Server) updateDocumentCategory(c *gin.Context) {
	var uri documentURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req saveDocumentCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	before, err := s.store.GetDocumentCategory(c, uri.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "document category")
			return
		}
		internalServerError(c, err)
		return
	}
	slug, err := s.checkedDocumentCategory(c, &req, uri.ID, before.Slug)
	if err != nil {
		badRequestError(c, err)
		return
	}
	row, err := s.store.UpdateDocumentCategory(c, db.UpdateDocumentCategoryParams{
		ID:          uri.ID,
		Name:        req.Name,
		Slug:        slug,
		Description: req.Description,
		Color:       req.Color,
		Icon:        req.Icon,
		ParentID:    documentNullIDPtr(req.ParentID),
		Position:    req.Position,
	})
	if err != nil {
		if documentConflictFromDatabase(c, err, "this category already exists") {
			return
		}
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, documentCategoryFromSaved(row))
}

type archiveDocumentCategoryRequest struct {
	Archived bool `json:"archived"`
}

// PUT /v1/admin/document-categories/:id/archive {"archived": true|false}
func (s *Server) archiveDocumentCategory(c *gin.Context) {
	var uri documentURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req archiveDocumentCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	row, err := s.store.ArchiveDocumentCategory(c, db.ArchiveDocumentCategoryParams{
		ID:       uri.ID,
		Archived: req.Archived,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "document category")
			return
		}
		if documentConflictFromDatabase(c, err, "a category with this name already exists") {
			return
		}
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, documentCategoryFromSaved(row))
}

func (s *Server) createDocumentCategoryFromName(c *gin.Context, name string) (db.DocumentCategory, error) {
	base := documentSlugify(name)
	if base == "" {
		base = "categorie"
	}
	candidate := base
	for suffix := 2; suffix < 1000; suffix++ {
		existing, err := s.store.GetDocumentCategoryBySlugAny(c, candidate)
		if errors.Is(err, sql.ErrNoRows) {
			return s.store.CreateDocumentCategory(c, db.CreateDocumentCategoryParams{
				Name:     name,
				Slug:     candidate,
				Position: 100,
			})
		}
		if err != nil {
			return db.DocumentCategory{}, err
		}
		if fold(existing.Name) == fold(name) {
			return existing, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
	return db.DocumentCategory{}, errors.New("cannot find a free category slug")
}
