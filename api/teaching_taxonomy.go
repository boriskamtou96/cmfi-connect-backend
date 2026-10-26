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

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns "Camp des jeunes" into "camp-des-jeunes", accents left aside.
func slugify(value string) string {
	return strings.Trim(nonSlug.ReplaceAllString(fold(value), "-"), "-")
}

// nullIDPtr: an optional id, 0 and null meaning none.
func nullIDPtr(id *int64) sql.NullInt64 {
	if id == nil || *id == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *id, Valid: true}
}

// The categories and the annual editions of the teachings. The admins create
// and rename them; the members see the categories in the filter chips. An
// archived category keeps its teachings but leaves the forms.

type adminTeachingCategoryResponse struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Slug          string    `json:"slug"`
	Description   string    `json:"description"`
	Color         string    `json:"color"`
	Icon          string    `json:"icon"`
	ParentID      *int64    `json:"parent_id"`
	Annual        bool      `json:"annual"`
	Position      int32     `json:"position"`
	Archived      bool      `json:"archived"`
	TeachingCount int32     `json:"teaching_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func toAdminTeachingCategory(row db.ListTeachingCategoriesRow) adminTeachingCategoryResponse {
	return adminTeachingCategoryResponse{
		ID:            row.ID,
		Name:          row.Name,
		Slug:          row.Slug,
		Description:   row.Description,
		Color:         row.Color,
		Icon:          row.Icon,
		ParentID:      nullInt64Ptr(row.ParentID),
		Annual:        row.Annual,
		Position:      row.Position,
		Archived:      row.ArchivedAt.Valid,
		TeachingCount: row.TeachingCount,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}

func categoryFromSaved(row db.TeachingCategory) adminTeachingCategoryResponse {
	return adminTeachingCategoryResponse{
		ID:          row.ID,
		Name:        row.Name,
		Slug:        row.Slug,
		Description: row.Description,
		Color:       row.Color,
		Icon:        row.Icon,
		ParentID:    nullInt64Ptr(row.ParentID),
		Annual:      row.Annual,
		Position:    row.Position,
		Archived:    row.ArchivedAt.Valid,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

// GET /v1/admin/teaching-categories: every category, archived ones too, with
// how many teachings each (drafts included).
func (s *Server) adminListTeachingCategories(c *gin.Context) {
	rows, err := s.store.ListTeachingCategories(c, db.ListTeachingCategoriesParams{
		WithDrafts:   true,
		WithArchived: true,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	categories := make([]adminTeachingCategoryResponse, 0, len(rows))
	for _, row := range rows {
		categories = append(categories, toAdminTeachingCategory(row))
	}
	apiResponse(c, http.StatusOK, categories)
}

type saveTeachingCategoryRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Color       string `json:"color"`
	Icon        string `json:"icon"`
	ParentID    *int64 `json:"parent_id"`
	Annual      bool   `json:"annual"`
	Position    int32  `json:"position"`
}

// checkedTeachingCategory: the name and the lengths checked; the slug is the
// one given, else the one it already had, else the name's. A slug stays the
// same when a category is renamed: the links of the apps keep working.
func (s *Server) checkedTeachingCategory(c *gin.Context, req *saveTeachingCategoryRequest, id int64, currentSlug string) (slug string, err error) {
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
	slug = slugify(req.Slug)
	if slug == "" {
		slug = currentSlug
	}
	if slug == "" {
		slug = slugify(req.Name)
	}
	if slug == "" {
		slug = "categorie"
	}
	if req.ParentID != nil && *req.ParentID != 0 {
		if *req.ParentID == id {
			return "", errors.New("parent_id: a category cannot contain itself")
		}
		parent, err := s.store.GetTeachingCategory(c, *req.ParentID)
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
	// the same name, or the same slug, can't be used twice
	byName, err := s.store.GetTeachingCategoryByName(c, req.Name)
	if err == nil && byName.ID != id {
		return "", errors.New("a category with this name already exists")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	bySlug, err := s.store.GetTeachingCategoryBySlugAny(c, slug)
	if err == nil && bySlug.ID != id {
		return "", errors.New("a category with this slug already exists")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return slug, nil
}

func conflictFromDatabase(c *gin.Context, err error, message string) bool {
	var databaseError *pq.Error
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		conflictError(c, errors.New(message))
		return true
	}
	return false
}

// POST /v1/admin/teaching-categories
func (s *Server) createTeachingCategory(c *gin.Context) {
	var req saveTeachingCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	slug, err := s.checkedTeachingCategory(c, &req, 0, "")
	if err != nil {
		badRequestError(c, err)
		return
	}
	row, err := s.store.CreateTeachingCategory(c, db.CreateTeachingCategoryParams{
		Name:        req.Name,
		Slug:        slug,
		Description: req.Description,
		Color:       req.Color,
		Icon:        req.Icon,
		ParentID:    nullIDPtr(req.ParentID),
		Annual:      req.Annual,
		Position:    req.Position,
	})
	if err != nil {
		if conflictFromDatabase(c, err, "this category already exists") {
			return
		}
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusCreated, categoryFromSaved(row))
}

// PUT /v1/admin/teaching-categories/:id
func (s *Server) updateTeachingCategory(c *gin.Context) {
	var uri teachingURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req saveTeachingCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	before, err := s.store.GetTeachingCategory(c, uri.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "teaching category")
			return
		}
		internalServerError(c, err)
		return
	}
	slug, err := s.checkedTeachingCategory(c, &req, uri.ID, before.Slug)
	if err != nil {
		badRequestError(c, err)
		return
	}
	row, err := s.store.UpdateTeachingCategory(c, db.UpdateTeachingCategoryParams{
		ID:          uri.ID,
		Name:        req.Name,
		Slug:        slug,
		Description: req.Description,
		Color:       req.Color,
		Icon:        req.Icon,
		ParentID:    nullIDPtr(req.ParentID),
		Annual:      req.Annual,
		Position:    req.Position,
	})
	if err != nil {
		if conflictFromDatabase(c, err, "this category already exists") {
			return
		}
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, categoryFromSaved(row))
}

type archiveCategoryRequest struct {
	Archived bool `json:"archived"`
}

// PUT /v1/admin/teaching-categories/:id/archive {"archived": true|false}
func (s *Server) archiveTeachingCategory(c *gin.Context) {
	var uri teachingURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req archiveCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	row, err := s.store.ArchiveTeachingCategory(c, db.ArchiveTeachingCategoryParams{
		ID:       uri.ID,
		Archived: req.Archived,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "teaching category")
			return
		}
		if conflictFromDatabase(c, err, "a category with this name already exists") {
			return
		}
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, categoryFromSaved(row))
}

func (s *Server) createTeachingCategoryFromName(c *gin.Context, name string) (db.TeachingCategory, error) {
	base := slugify(name)
	if base == "" {
		base = "categorie"
	}
	candidate := base
	for suffix := 2; suffix < 1000; suffix++ {
		existing, err := s.store.GetTeachingCategoryBySlugAny(c, candidate)
		if errors.Is(err, sql.ErrNoRows) {
			return s.store.CreateTeachingCategory(c, db.CreateTeachingCategoryParams{
				Name:     name,
				Slug:     candidate,
				Annual:   false,
				Position: 100,
			})
		}
		if err != nil {
			return db.TeachingCategory{}, err
		}
		if fold(existing.Name) == fold(name) {
			return existing, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
	return db.TeachingCategory{}, errors.New("cannot find a free category slug")
}

// --- annual editions -------------------------------------------------------

type teachingEditionResponse struct {
	ID            int64          `json:"id"`
	CategoryID    int64          `json:"category_id"`
	CategoryName  string         `json:"category_name"`
	CategorySlug  string         `json:"category_slug"`
	Year          int32          `json:"year"`
	Title         string         `json:"title"`
	Theme         string         `json:"theme"`
	StartsOn      *string        `json:"starts_on"`
	EndsOn        *string        `json:"ends_on"`
	Venue         string         `json:"venue"`
	Cover         *mediaResponse `json:"cover"`
	TeachingCount int32          `json:"teaching_count"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

func toTeachingEdition(row db.ListTeachingEditionsRow, covers map[int64]mediaResponse) teachingEditionResponse {
	response := teachingEditionResponse{
		ID:            row.ID,
		CategoryID:    row.CategoryID,
		CategoryName:  row.CategoryName,
		CategorySlug:  row.CategorySlug,
		Year:          int32(row.Year),
		Title:         row.Title,
		Theme:         row.Theme,
		Venue:         row.Venue,
		TeachingCount: row.TeachingCount,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
	if row.StartsOn.Valid {
		day := row.StartsOn.Time.Format(time.DateOnly)
		response.StartsOn = &day
	}
	if row.EndsOn.Valid {
		day := row.EndsOn.Time.Format(time.DateOnly)
		response.EndsOn = &day
	}
	if cover, ok := covers[row.CoverID.Int64]; ok && row.CoverID.Valid {
		response.Cover = &cover
	}
	return response
}

// GET /v1/admin/teaching-editions: the annual editions, newest year first.
func (s *Server) adminListTeachingEditions(c *gin.Context) {
	rows, err := s.store.ListTeachingEditions(c, true)
	if err != nil {
		internalServerError(c, err)
		return
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.CoverID.Int64)
	}
	covers, err := s.mediaByIDs(c, ids)
	if err != nil {
		internalServerError(c, err)
		return
	}
	editions := make([]teachingEditionResponse, 0, len(rows))
	for _, row := range rows {
		editions = append(editions, toTeachingEdition(row, covers))
	}
	apiResponse(c, http.StatusOK, editions)
}

func (s *Server) teachingEditionByID(c *gin.Context, status int, id int64) {
	row, err := s.store.GetTeachingEdition(c, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "teaching edition")
			return
		}
		internalServerError(c, err)
		return
	}
	category, err := s.store.GetTeachingCategory(c, row.CategoryID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	covers, err := s.mediaByIDs(c, []int64{row.CoverID.Int64})
	if err != nil {
		internalServerError(c, err)
		return
	}
	response := teachingEditionResponse{
		ID:           row.ID,
		CategoryID:   row.CategoryID,
		CategoryName: category.Name,
		CategorySlug: category.Slug,
		Year:         int32(row.Year),
		Title:        row.Title,
		Theme:        row.Theme,
		Venue:        row.Venue,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}
	if row.StartsOn.Valid {
		day := row.StartsOn.Time.Format(time.DateOnly)
		response.StartsOn = &day
	}
	if row.EndsOn.Valid {
		day := row.EndsOn.Time.Format(time.DateOnly)
		response.EndsOn = &day
	}
	if cover, ok := covers[row.CoverID.Int64]; ok && row.CoverID.Valid {
		response.Cover = &cover
	}
	apiResponse(c, status, response)
}

type saveTeachingEditionRequest struct {
	CategoryID int64   `json:"category_id"`
	Year       int32   `json:"year"`
	Title      string  `json:"title"`
	Theme      string  `json:"theme"`
	StartsOn   *string `json:"starts_on"`
	EndsOn     *string `json:"ends_on"`
	Venue      string  `json:"venue"`
	CoverID    *int64  `json:"cover_id"`
}

func (s *Server) checkedTeachingEdition(c *gin.Context, req *saveTeachingEditionRequest) (starts, ends sql.NullTime, cover sql.NullInt64, err error) {
	req.Title = strings.TrimSpace(req.Title)
	req.Theme = strings.TrimSpace(req.Theme)
	req.Venue = strings.TrimSpace(req.Venue)
	switch {
	case req.CategoryID == 0:
		return starts, ends, cover, errors.New("category_id is required")
	case req.Year < 1900 || req.Year > 2200:
		return starts, ends, cover, errors.New("year: 1900 to 2200")
	case utf8.RuneCountInString(req.Title) > 200:
		return starts, ends, cover, errors.New("title: 200 characters at most")
	case utf8.RuneCountInString(req.Theme) > 300:
		return starts, ends, cover, errors.New("theme: 300 characters at most")
	case utf8.RuneCountInString(req.Venue) > 200:
		return starts, ends, cover, errors.New("venue: 200 characters at most")
	}
	category, err := s.store.GetTeachingCategory(c, req.CategoryID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return starts, ends, cover, errors.New("category_id: unknown category")
		}
		return starts, ends, cover, err
	}
	if req.Title == "" {
		req.Title = fmt.Sprintf("%s %d", category.Name, req.Year)
	}
	if req.StartsOn != nil && strings.TrimSpace(*req.StartsOn) != "" {
		day, err := time.Parse(time.DateOnly, strings.TrimSpace(*req.StartsOn))
		if err != nil {
			return starts, ends, cover, errors.New("starts_on: a day such as 2026-08-01")
		}
		starts = sql.NullTime{Time: day, Valid: true}
	}
	if req.EndsOn != nil && strings.TrimSpace(*req.EndsOn) != "" {
		day, err := time.Parse(time.DateOnly, strings.TrimSpace(*req.EndsOn))
		if err != nil {
			return starts, ends, cover, errors.New("ends_on: a day such as 2026-08-07")
		}
		ends = sql.NullTime{Time: day, Valid: true}
	}
	if starts.Valid && ends.Valid && ends.Time.Before(starts.Time) {
		return starts, ends, cover, errors.New("ends_on: after starts_on")
	}
	if req.CoverID != nil && *req.CoverID != 0 {
		rows, err := s.store.ListMediaByIDs(c, []int64{*req.CoverID})
		if err != nil {
			return starts, ends, cover, err
		}
		if len(rows) == 0 || !strings.HasPrefix(rows[0].ContentType, "image/") {
			return starts, ends, cover, errors.New("cover_id: not an image")
		}
		cover = sql.NullInt64{Int64: *req.CoverID, Valid: true}
	}
	return starts, ends, cover, nil
}

// POST /v1/admin/teaching-editions
func (s *Server) createTeachingEdition(c *gin.Context) {
	var req saveTeachingEditionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	starts, ends, cover, err := s.checkedTeachingEdition(c, &req)
	if err != nil {
		badRequestError(c, err)
		return
	}
	row, err := s.store.CreateTeachingEdition(c, db.CreateTeachingEditionParams{
		CategoryID: req.CategoryID,
		Year:       int16(req.Year),
		Title:      req.Title,
		Theme:      req.Theme,
		StartsOn:   starts,
		EndsOn:     ends,
		Venue:      req.Venue,
		CoverID:    cover,
	})
	if err != nil {
		if conflictFromDatabase(c, err, "this category already has an edition for this year") {
			return
		}
		internalServerError(c, err)
		return
	}
	s.teachingEditionByID(c, http.StatusCreated, row.ID)
}

// PUT /v1/admin/teaching-editions/:id
func (s *Server) updateTeachingEdition(c *gin.Context) {
	var uri teachingURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req saveTeachingEditionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if _, err := s.store.GetTeachingEdition(c, uri.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "teaching edition")
			return
		}
		internalServerError(c, err)
		return
	}
	starts, ends, cover, err := s.checkedTeachingEdition(c, &req)
	if err != nil {
		badRequestError(c, err)
		return
	}
	if _, err := s.store.UpdateTeachingEdition(c, db.UpdateTeachingEditionParams{
		ID:         uri.ID,
		CategoryID: req.CategoryID,
		Year:       int16(req.Year),
		Title:      req.Title,
		Theme:      req.Theme,
		StartsOn:   starts,
		EndsOn:     ends,
		Venue:      req.Venue,
		CoverID:    cover,
	}); err != nil {
		if conflictFromDatabase(c, err, "this category already has an edition for this year") {
			return
		}
		internalServerError(c, err)
		return
	}
	s.teachingEditionByID(c, http.StatusOK, uri.ID)
}

// DELETE /v1/admin/teaching-editions/:id: only when no teaching uses it.
func (s *Server) deleteTeachingEdition(c *gin.Context) {
	var uri teachingURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	count, err := s.store.CountTeachingsInEdition(c, sql.NullInt64{Int64: uri.ID, Valid: true})
	if err != nil {
		internalServerError(c, err)
		return
	}
	if count > 0 {
		conflictError(c, errors.New("this edition still has teachings"))
		return
	}
	if _, err := s.store.DeleteTeachingEdition(c, uri.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "teaching edition")
			return
		}
		internalServerError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
