package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/media"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// Enseignements: the audio messages of the community. The admins send an
// MP3 or an M4A, give it a title, the speaker, a category, the day it was
// given and a cover if they like, and publish it; every member hears the
// published ones, the newest message first, a page at a time, by category or
// by a search on their words. The apps stream the file from its link (the
// CDN in production) and may keep it to listen offline.

type teachingAudioResponse struct {
	ID  int64  `json:"id"`
	URL string `json:"url"`
	// mp3 or m4a
	Format    string `json:"format"`
	SizeBytes int64  `json:"size_bytes"`
}

type teachingResponse struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Speaker     string `json:"speaker"`
	Description string `json:"description"`
	Category    string `json:"category"`
	// "2024-08-15"; null when unknown
	RecordedOn      *string               `json:"recorded_on"`
	Audio           teachingAudioResponse `json:"audio"`
	DurationSeconds int32                 `json:"duration_seconds"`
	// null without one
	Cover       *mediaResponse `json:"cover"`
	Published   bool           `json:"published"`
	PublishedAt *time.Time     `json:"published_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	// when the members were told by a push; null before
	NotifiedAt *time.Time `json:"notified_at"`
}

func audioFormat(contentType string) string {
	if contentType == media.M4A {
		return "m4a"
	}
	return "mp3"
}

func toTeaching(row db.ListTeachingsRow, covers map[int64]mediaResponse) teachingResponse {
	response := teachingResponse{
		ID:          row.ID,
		Title:       row.Title,
		Speaker:     row.Speaker,
		Description: row.Description,
		Category:    row.Category,
		Audio: teachingAudioResponse{
			ID:        row.AudioID,
			URL:       mediaURL(row.AudioKey),
			Format:    audioFormat(row.AudioType),
			SizeBytes: row.AudioSize,
		},
		DurationSeconds: row.DurationSeconds,
		Published:       row.Published,
		UpdatedAt:       row.UpdatedAt,
	}
	if row.RecordedOn.Valid {
		day := row.RecordedOn.Time.Format(time.DateOnly)
		response.RecordedOn = &day
	}
	if row.PublishedAt.Valid {
		at := row.PublishedAt.Time
		response.PublishedAt = &at
	}
	response.NotifiedAt = timePtr(row.NotifiedAt)
	if cover, ok := covers[row.CoverID.Int64]; ok && row.CoverID.Valid {
		response.Cover = &cover
	}
	return response
}

type teachingsRequest struct {
	pageRequest
	// words of the title, the speaker, the description or the category; case
	// and accents left aside
	Search string `form:"search"`
	// one category; empty for all
	Category string `form:"category"`
}

// keeps tells whether a teaching answers the category and the search.
func (r teachingsRequest) keeps(t teachingResponse) bool {
	if category := fold(r.Category); category != "" && fold(t.Category) != category {
		return false
	}
	words := strings.Fields(fold(r.Search))
	if len(words) == 0 {
		return true
	}
	text := fold(strings.Join([]string{t.Title, t.Speaker, t.Description, t.Category}, " "))
	for _, word := range words {
		if !strings.Contains(text, word) {
			return false
		}
	}
	return true
}

// GET /v1/teachings?search=&category=&page=&size=: the published teachings,
// the newest message first.
func (s *Server) listTeachings(c *gin.Context) {
	s.teachings(c, false)
}

// GET /v1/admin/teachings?search=&category=&page=&size=: the drafts too
// (after the published ones).
func (s *Server) adminListTeachings(c *gin.Context) {
	s.teachings(c, true)
}

func (s *Server) teachings(c *gin.Context, withDrafts bool) {
	var req teachingsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		badRequestError(c, err)
		return
	}
	rows, err := s.store.ListTeachings(c, withDrafts)
	if err != nil {
		internalServerError(c, err)
		return
	}
	// filtered without the covers, which only the page needs
	kept := []db.ListTeachingsRow{}
	for _, row := range rows {
		if req.keeps(toTeaching(row, nil)) {
			kept = append(kept, row)
		}
	}
	limit, offset := req.limits()
	page := kept[min(int(offset), len(kept)):min(int(offset+limit), len(kept))]
	ids := make([]int64, 0, len(page))
	for _, row := range page {
		ids = append(ids, row.CoverID.Int64)
	}
	covers, err := s.mediaByIDs(c, ids)
	if err != nil {
		internalServerError(c, err)
		return
	}
	teachings := make([]teachingResponse, 0, len(page))
	for _, row := range page {
		teachings = append(teachings, toTeaching(row, covers))
	}
	paginatedApiResponse(c, http.StatusOK, teachings, MetaResponse{
		Total:  int64(len(kept)),
		Limit:  limit,
		Offset: offset,
	})
}

type teachingCategoryResponse struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// GET /v1/teachings/categories: the categories of the published teachings,
// with how many each, in alphabetical order (the filter chips of the app).
func (s *Server) listTeachingCategories(c *gin.Context) {
	rows, err := s.store.ListTeachings(c, false)
	if err != nil {
		internalServerError(c, err)
		return
	}
	counts := map[string]*teachingCategoryResponse{}
	for _, row := range rows {
		name := strings.TrimSpace(row.Category)
		if name == "" {
			continue
		}
		// "Prédications" and "prédications" are one
		key := fold(name)
		if counts[key] == nil {
			counts[key] = &teachingCategoryResponse{Name: name}
		}
		counts[key].Count++
	}
	categories := make([]teachingCategoryResponse, 0, len(counts))
	for _, category := range counts {
		categories = append(categories, *category)
	}
	sort.Slice(categories, func(i, j int) bool {
		return fold(categories[i].Name) < fold(categories[j].Name)
	})
	apiResponse(c, http.StatusOK, categories)
}

// POST /v1/admin/teachings/files (multipart form, field "file"): an MP3 or
// an M4A of 100 MB at most, before the teaching that lists it. The cover is
// an image sent to POST /v1/admin/media.
func (s *Server) uploadTeachingAudio(c *gin.Context) {
	tooLarge := func() {
		c.JSON(http.StatusRequestEntityTooLarge, NewErrorResponse(http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE",
			fmt.Errorf("sounds up to %d MB", media.MaxAudioSize>>20)))
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, media.MaxAudioSize+1<<20)
	header, err := c.FormFile("file")
	if err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			tooLarge()
			return
		}
		badRequestError(c, err)
		return
	}
	if header.Size > media.MaxAudioSize {
		tooLarge()
		return
	}
	file, err := header.Open()
	if err != nil {
		badRequestError(c, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, media.MaxAudioSize+1))
	if err != nil {
		badRequestError(c, err)
		return
	}
	info, err := media.InspectAudio(data, header.Filename)
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
	s.compressLater(row.ID, key, data)
	apiResponse(c, http.StatusCreated, teachingAudioResponse{
		ID:        row.ID,
		URL:       mediaURL(row.Key),
		Format:    audioFormat(row.ContentType),
		SizeBytes: row.SizeBytes,
	})
}

type saveTeachingRequest struct {
	Title       string `json:"title"`
	Speaker     string `json:"speaker"`
	Description string `json:"description"`
	Category    string `json:"category"`
	// "2024-08-15"; empty or null when unknown
	RecordedOn      *string `json:"recorded_on"`
	AudioID         int64   `json:"audio_id"`
	DurationSeconds int32   `json:"duration_seconds"`
	// null or 0: no cover
	CoverID   *int64 `json:"cover_id"`
	Published bool   `json:"published"`
	// « Prévenir les membres » the first time it is published (true when
	// absent)
	Notify *bool `json:"notify"`
}

// checkedTeaching: trimmed, the lengths, the sound an MP3 or an M4A and the
// cover an image; the values to save.
func (s *Server) checkedTeaching(c *gin.Context, req *saveTeachingRequest) (recordedOn sql.NullTime, cover sql.NullInt64, err error) {
	req.Title = strings.TrimSpace(req.Title)
	req.Speaker = strings.Join(strings.Fields(req.Speaker), " ")
	req.Description = strings.TrimSpace(req.Description)
	req.Category = strings.Join(strings.Fields(req.Category), " ")
	switch {
	case req.Title == "":
		return recordedOn, cover, errors.New("title is required")
	case utf8.RuneCountInString(req.Title) > 200:
		return recordedOn, cover, errors.New("title: 200 characters at most")
	case utf8.RuneCountInString(req.Speaker) > 120:
		return recordedOn, cover, errors.New("speaker: 120 characters at most")
	case utf8.RuneCountInString(req.Description) > 4000:
		return recordedOn, cover, errors.New("description: 4000 characters at most")
	case utf8.RuneCountInString(req.Category) > 60:
		return recordedOn, cover, errors.New("category: 60 characters at most")
	case req.DurationSeconds < 0 || req.DurationSeconds > 24*3600:
		return recordedOn, cover, errors.New("duration_seconds: 0 to 86400")
	case req.AudioID == 0:
		return recordedOn, cover, errors.New("audio_id is required")
	}
	if req.RecordedOn != nil && strings.TrimSpace(*req.RecordedOn) != "" {
		day, err := time.Parse(time.DateOnly, strings.TrimSpace(*req.RecordedOn))
		if err != nil || day.Year() < 1900 || day.After(time.Now().AddDate(0, 0, 1)) {
			return recordedOn, cover, errors.New("recorded_on: a day such as 2024-08-15, not in the future")
		}
		recordedOn = sql.NullTime{Time: day, Valid: true}
	}
	ids := []int64{req.AudioID}
	if req.CoverID != nil && *req.CoverID != 0 {
		ids = append(ids, *req.CoverID)
	}
	rows, err := s.store.ListMediaByIDs(c, ids)
	if err != nil {
		return recordedOn, cover, err
	}
	types := map[int64]string{}
	for _, row := range rows {
		types[row.ID] = row.ContentType
	}
	if !(media.Info{ContentType: types[req.AudioID]}).IsAudio() {
		return recordedOn, cover, fmt.Errorf("audio_id %d is not an MP3 nor an M4A", req.AudioID)
	}
	if len(ids) == 2 {
		if !strings.HasPrefix(types[ids[1]], "image/") {
			return recordedOn, cover, fmt.Errorf("cover_id %d is not an image", ids[1])
		}
		cover = sql.NullInt64{Int64: ids[1], Valid: true}
	}
	return recordedOn, cover, nil
}

// teachingByID answers one teaching with its cover.
func (s *Server) teachingByID(c *gin.Context, status int, id int64) {
	row, err := s.store.GetTeaching(c, id)
	if err != nil {
		internalServerError(c, err)
		return
	}
	covers, err := s.mediaByIDs(c, []int64{row.CoverID.Int64})
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, status, toTeaching(db.ListTeachingsRow(row), covers))
}

// POST /v1/admin/teachings
func (s *Server) createTeaching(c *gin.Context) {
	var req saveTeachingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	recordedOn, cover, err := s.checkedTeaching(c, &req)
	if err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	id, err := s.store.CreateTeaching(c, db.CreateTeachingParams{
		Title:           req.Title,
		Speaker:         req.Speaker,
		Description:     req.Description,
		Category:        req.Category,
		RecordedOn:      recordedOn,
		AudioID:         req.AudioID,
		DurationSeconds: req.DurationSeconds,
		CoverID:         cover,
		Published:       req.Published,
		CreatedBy:       nullID(authUser.ID),
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	s.announceTeaching(c, id, req.Notify)
	s.teachingByID(c, http.StatusCreated, id)
}

type teachingURI struct {
	ID int64 `uri:"id" binding:"required,min=1"`
}

// PUT /v1/admin/teachings/:id: everything at once; a sound or a cover
// replaced is deleted.
func (s *Server) updateTeaching(c *gin.Context) {
	var uri teachingURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req saveTeachingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	before, err := s.store.GetTeaching(c, uri.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "teaching")
			return
		}
		internalServerError(c, err)
		return
	}
	recordedOn, cover, err := s.checkedTeaching(c, &req)
	if err != nil {
		badRequestError(c, err)
		return
	}
	if _, err := s.store.UpdateTeaching(c, db.UpdateTeachingParams{
		ID:              uri.ID,
		Title:           req.Title,
		Speaker:         req.Speaker,
		Description:     req.Description,
		Category:        req.Category,
		RecordedOn:      recordedOn,
		AudioID:         req.AudioID,
		DurationSeconds: req.DurationSeconds,
		CoverID:         cover,
		Published:       req.Published,
	}); err != nil {
		internalServerError(c, err)
		return
	}
	if before.AudioID != req.AudioID {
		s.dropTeachingMedia(c, before.AudioID)
	}
	if before.CoverID.Valid && before.CoverID != cover {
		s.dropTeachingMedia(c, before.CoverID.Int64)
	}
	s.announceTeaching(c, uri.ID, req.Notify)
	s.teachingByID(c, http.StatusOK, uri.ID)
}

// DELETE /v1/admin/teachings/:id: the teaching, its sound and its cover.
func (s *Server) deleteTeaching(c *gin.Context) {
	var uri teachingURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	row, err := s.store.DeleteTeaching(c, uri.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "teaching")
			return
		}
		internalServerError(c, err)
		return
	}
	s.dropTeachingMedia(c, row.AudioID)
	if row.CoverID.Valid {
		s.dropTeachingMedia(c, row.CoverID.Int64)
	}
	c.Status(http.StatusNoContent)
}

// dropTeachingMedia deletes a sound or a cover no teaching uses any more.
func (s *Server) dropTeachingMedia(c *gin.Context, id int64) {
	count, err := s.store.CountTeachingsWithMedia(c, id)
	if err != nil {
		logFor(c).Error("cannot count the teachings of a media", "media_id", id, "error", err)
		return
	}
	if count == 0 {
		s.dropMedia(c, id)
	}
}
