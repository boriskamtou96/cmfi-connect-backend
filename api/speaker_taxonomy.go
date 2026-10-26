package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
)

// The speakers of the teachings: a fiche per person (name, role, bio, photo),
// created and renamed by the admins, merged when the same person was typed
// twice. The apps filter the list by speaker_id; the text column of the
// teachings follows the fiche, so an app that only knows the name keeps
// working.

type adminTeachingSpeakerResponse struct {
	ID            int64          `json:"id"`
	Name          string         `json:"name"`
	Slug          string         `json:"slug"`
	Role          string         `json:"role"`
	Bio           string         `json:"bio"`
	Photo         *mediaResponse `json:"photo"`
	Archived      bool           `json:"archived"`
	TeachingCount int32          `json:"teaching_count"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

func toAdminTeachingSpeaker(row db.ListTeachingSpeakersRow, photos map[int64]mediaResponse) adminTeachingSpeakerResponse {
	response := adminTeachingSpeakerResponse{
		ID:            row.ID,
		Name:          row.Name,
		Slug:          row.Slug,
		Role:          row.Role,
		Bio:           row.Bio,
		Archived:      row.ArchivedAt.Valid,
		TeachingCount: row.TeachingCount,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
	if photo, ok := photos[row.PhotoID.Int64]; ok && row.PhotoID.Valid {
		response.Photo = &photo
	}
	return response
}

// speakerFromSaved answers one saved fiche, its photo and its teachings.
func (s *Server) speakerFromSaved(c *gin.Context, row db.TeachingSpeaker) (adminTeachingSpeakerResponse, error) {
	photos, err := s.mediaByIDs(c, []int64{row.PhotoID.Int64})
	if err != nil {
		return adminTeachingSpeakerResponse{}, err
	}
	count, err := s.store.CountTeachingsOfSpeaker(c, sql.NullInt64{Int64: row.ID, Valid: true})
	if err != nil {
		return adminTeachingSpeakerResponse{}, err
	}
	response := adminTeachingSpeakerResponse{
		ID:            row.ID,
		Name:          row.Name,
		Slug:          row.Slug,
		Role:          row.Role,
		Bio:           row.Bio,
		Archived:      row.ArchivedAt.Valid,
		TeachingCount: int32(count),
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
	if photo, ok := photos[row.PhotoID.Int64]; ok && row.PhotoID.Valid {
		response.Photo = &photo
	}
	return response, nil
}

// GET /v1/admin/speakers: every speaker, archived ones too, with how many
// teachings each (drafts included).
func (s *Server) adminListTeachingSpeakers(c *gin.Context) {
	rows, err := s.store.ListTeachingSpeakers(c, db.ListTeachingSpeakersParams{
		WithDrafts:   true,
		WithArchived: true,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.PhotoID.Int64)
	}
	photos, err := s.mediaByIDs(c, ids)
	if err != nil {
		internalServerError(c, err)
		return
	}
	speakers := make([]adminTeachingSpeakerResponse, 0, len(rows))
	for _, row := range rows {
		speakers = append(speakers, toAdminTeachingSpeaker(row, photos))
	}
	apiResponse(c, http.StatusOK, speakers)
}

type teachingSpeakerResponse struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Count int    `json:"count"`
}

// GET /v1/teachings/speakers: the speakers of the published teachings, with
// how many each (the filter of the apps).
func (s *Server) listTeachingSpeakers(c *gin.Context) {
	rows, err := s.store.ListTeachingSpeakers(c, db.ListTeachingSpeakersParams{
		WithDrafts:   false,
		WithArchived: false,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	speakers := make([]teachingSpeakerResponse, 0, len(rows))
	for _, row := range rows {
		if row.TeachingCount == 0 {
			continue
		}
		speakers = append(speakers, teachingSpeakerResponse{
			ID: row.ID, Name: row.Name, Slug: row.Slug, Count: int(row.TeachingCount),
		})
	}
	apiResponse(c, http.StatusOK, speakers)
}

type saveTeachingSpeakerRequest struct {
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Role    string `json:"role"`
	Bio     string `json:"bio"`
	PhotoID *int64 `json:"photo_id"`
}

// checkedTeachingSpeaker checks the name and the lengths, and keeps the slug
// of a renamed speaker: the links of the apps keep working. It answers the
// slug to save and the photo checked.
func (s *Server) checkedTeachingSpeaker(c *gin.Context, req *saveTeachingSpeakerRequest, id int64, currentSlug string) (slug string, photo sql.NullInt64, err error) {
	req.Name = strings.Join(strings.Fields(req.Name), " ")
	req.Role = strings.Join(strings.Fields(req.Role), " ")
	req.Bio = strings.TrimSpace(req.Bio)
	switch {
	case req.Name == "":
		return "", photo, errors.New("name is required")
	case utf8.RuneCountInString(req.Name) > 120:
		return "", photo, errors.New("name: 120 characters at most")
	case utf8.RuneCountInString(req.Role) > 80:
		return "", photo, errors.New("role: 80 characters at most")
	case utf8.RuneCountInString(req.Bio) > 2000:
		return "", photo, errors.New("bio: 2000 characters at most")
	}
	slug = slugify(req.Slug)
	if slug == "" {
		slug = currentSlug
	}
	if slug == "" {
		slug = slugify(req.Name)
	}
	if slug == "" {
		slug = "orateur"
	}
	// the same name, or the same slug, can't be used twice
	byName, err := s.store.GetTeachingSpeakerByName(c, req.Name)
	if err == nil && byName.ID != id {
		return "", photo, errors.New("a speaker with this name already exists")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", photo, err
	}
	bySlug, err := s.store.GetTeachingSpeakerBySlug(c, slug)
	if err == nil && bySlug.ID != id {
		return "", photo, errors.New("a speaker with this slug already exists")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", photo, err
	}
	if req.PhotoID != nil && *req.PhotoID != 0 {
		rows, err := s.store.ListMediaByIDs(c, []int64{*req.PhotoID})
		if err != nil {
			return "", photo, err
		}
		if len(rows) == 0 || !strings.HasPrefix(rows[0].ContentType, "image/") {
			return "", photo, errors.New("photo_id: not an image")
		}
		photo = sql.NullInt64{Int64: *req.PhotoID, Valid: true}
	}
	return slug, photo, nil
}

// POST /v1/admin/speakers
func (s *Server) createTeachingSpeaker(c *gin.Context) {
	var req saveTeachingSpeakerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	slug, photo, err := s.checkedTeachingSpeaker(c, &req, 0, "")
	if err != nil {
		badRequestError(c, err)
		return
	}
	row, err := s.store.CreateTeachingSpeaker(c, db.CreateTeachingSpeakerParams{
		Name:    req.Name,
		Slug:    slug,
		Role:    req.Role,
		Bio:     req.Bio,
		PhotoID: photo,
	})
	if err != nil {
		if conflictFromDatabase(c, err, "this speaker already exists") {
			return
		}
		internalServerError(c, err)
		return
	}
	response, err := s.speakerFromSaved(c, row)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusCreated, response)
}

// PUT /v1/admin/speakers/:id: the fiche; a rename follows in the text of every
// teaching of the speaker.
func (s *Server) updateTeachingSpeaker(c *gin.Context) {
	var uri teachingURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req saveTeachingSpeakerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	before, err := s.store.GetTeachingSpeaker(c, uri.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "speaker")
			return
		}
		internalServerError(c, err)
		return
	}
	slug, photo, err := s.checkedTeachingSpeaker(c, &req, uri.ID, before.Slug)
	if err != nil {
		badRequestError(c, err)
		return
	}
	row, err := s.store.UpdateTeachingSpeaker(c, db.UpdateTeachingSpeakerParams{
		ID:      uri.ID,
		Name:    req.Name,
		Slug:    slug,
		Role:    req.Role,
		Bio:     req.Bio,
		PhotoID: photo,
	})
	if err != nil {
		if conflictFromDatabase(c, err, "this speaker already exists") {
			return
		}
		internalServerError(c, err)
		return
	}
	response, err := s.speakerFromSaved(c, row)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, response)
}

type archiveSpeakerRequest struct {
	Archived bool `json:"archived"`
}

// PUT /v1/admin/speakers/:id/archive {"archived": true|false}
func (s *Server) archiveTeachingSpeaker(c *gin.Context) {
	var uri teachingURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req archiveSpeakerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	row, err := s.store.ArchiveTeachingSpeaker(c, db.ArchiveTeachingSpeakerParams{
		ID:       uri.ID,
		Archived: req.Archived,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "speaker")
			return
		}
		if conflictFromDatabase(c, err, "a speaker with this name already exists") {
			return
		}
		internalServerError(c, err)
		return
	}
	response, err := s.speakerFromSaved(c, row)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, response)
}

// DELETE /v1/admin/speakers/:id: only a speaker no teaching names; the others
// are archived, so the teachings keep their speaker.
func (s *Server) deleteTeachingSpeaker(c *gin.Context) {
	var uri teachingURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	if _, err := s.store.GetTeachingSpeaker(c, uri.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "speaker")
			return
		}
		internalServerError(c, err)
		return
	}
	count, err := s.store.CountTeachingsOfSpeaker(c, sql.NullInt64{Int64: uri.ID, Valid: true})
	if err != nil {
		internalServerError(c, err)
		return
	}
	if count > 0 {
		conflictError(c, errors.New("this speaker still names teachings: archive it instead"))
		return
	}
	if err := s.store.DeleteTeachingSpeaker(c, uri.ID); err != nil {
		internalServerError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type mergeTeachingSpeakersRequest struct {
	IntoID  int64   `json:"into_id"`
	FromIDs []int64 `json:"from_ids"`
}

// POST /v1/admin/speakers/merge {"into_id": 1, "from_ids": [2, 3]}: the
// teachings of the duplicates go to the one kept, and the duplicates are
// archived.
func (s *Server) mergeTeachingSpeakers(c *gin.Context) {
	var req mergeTeachingSpeakersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if req.IntoID == 0 {
		badRequestError(c, errors.New("into_id is required"))
		return
	}
	target, err := s.store.GetTeachingSpeaker(c, req.IntoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			badRequestError(c, errors.New("into_id: unknown speaker"))
			return
		}
		internalServerError(c, err)
		return
	}
	if target.ArchivedAt.Valid {
		badRequestError(c, errors.New("into_id: this speaker is archived"))
		return
	}
	from := make([]int64, 0, len(req.FromIDs))
	seen := map[int64]bool{req.IntoID: true}
	for _, id := range req.FromIDs {
		if id == 0 || seen[id] {
			continue
		}
		if _, err := s.store.GetTeachingSpeaker(c, id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				badRequestError(c, fmt.Errorf("from_ids: unknown speaker %d", id))
				return
			}
			internalServerError(c, err)
			return
		}
		seen[id] = true
		from = append(from, id)
	}
	if len(from) == 0 {
		badRequestError(c, errors.New("from_ids: nothing to merge"))
		return
	}
	if len(from) > 50 {
		badRequestError(c, errors.New("from_ids: 50 speakers at most"))
		return
	}
	if err := s.store.MergeTeachingSpeakers(c, db.MergeTeachingSpeakersParams{
		IntoID:  req.IntoID,
		FromIds: from,
	}); err != nil {
		internalServerError(c, err)
		return
	}
	response, err := s.speakerFromSaved(c, target)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, response)
}

// createTeachingSpeakerFromName makes the fiche of a name typed by an older
// app: its slug is the name's, numbered when taken.
func (s *Server) createTeachingSpeakerFromName(c *gin.Context, name string) (db.TeachingSpeaker, error) {
	base := slugify(name)
	if base == "" {
		base = "orateur"
	}
	candidate := base
	for suffix := 2; suffix < 1000; suffix++ {
		existing, err := s.store.GetTeachingSpeakerBySlug(c, candidate)
		if errors.Is(err, sql.ErrNoRows) {
			return s.store.CreateTeachingSpeaker(c, db.CreateTeachingSpeakerParams{
				Name: name,
				Slug: candidate,
			})
		}
		if err != nil {
			return db.TeachingSpeaker{}, err
		}
		if fold(existing.Name) == fold(name) {
			return existing, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
	return db.TeachingSpeaker{}, errors.New("cannot find a free speaker slug")
}
