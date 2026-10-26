package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
// published ones, the newest message first, a page at a time, by category,
// speaker, year or day, or by a search on their words. The apps stream the
// file from its link (the CDN in production) and may keep it to listen
// offline.

type teachingAudioResponse struct {
	ID  int64  `json:"id"`
	URL string `json:"url"`
	// mp3 or m4a
	Format    string `json:"format"`
	SizeBytes int64  `json:"size_bytes"`
}

type teachingEditionBrief struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Theme string `json:"theme"`
	Year  int32  `json:"year"`
}

type teachingResponse struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Speaker     string `json:"speaker"`
	Description string `json:"description"`
	// the category's name, kept for the apps that don't know category_id yet
	Category     string `json:"category"`
	CategoryID   *int64 `json:"category_id"`
	CategorySlug string `json:"category_slug"`
	// the year of the message ("2026"), null when unknown
	Year *int32 `json:"year"`
	// day 1, 2… of a camp or a crusade; several teachings may share it
	DayNumber *int32 `json:"day_number"`
	// their order inside the day
	Position int32 `json:"position"`
	// the annual edition ("Camp des jeunes 2026"), null when none
	Edition *teachingEditionBrief `json:"edition"`
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

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	number := value.Int64
	return &number
}

func nullInt16Ptr(value sql.NullInt16) *int32 {
	if !value.Valid {
		return nil
	}
	number := int32(value.Int16)
	return &number
}

func toTeaching(row db.TeachingDetail, covers map[int64]mediaResponse) teachingResponse {
	category := row.Category
	if row.CategoryName.Valid {
		category = row.CategoryName.String
	}
	response := teachingResponse{
		ID:           row.ID,
		Title:        row.Title,
		Speaker:      row.Speaker,
		Description:  row.Description,
		Category:     category,
		CategoryID:   nullInt64Ptr(row.CategoryID),
		CategorySlug: row.CategorySlug.String,
		Year:         nullInt16Ptr(row.Year),
		DayNumber:    nullInt16Ptr(row.DayNumber),
		Position:     row.Position,
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
	if row.EditionID.Valid {
		response.Edition = &teachingEditionBrief{
			ID:    row.EditionID.Int64,
			Title: row.EditionTitle.String,
			Theme: row.EditionTheme.String,
			Year:  int32(row.Year.Int16),
		}
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
	// words of the title, the speaker, the description, the category, the
	// edition or the year; case and accents left aside
	Search string `form:"search"`
	// the same, for the apps that ask q
	Q string `form:"q"`
	// one category: its id, its slug ("camp-des-jeunes") or its name
	Category   string `form:"category"`
	CategoryID int64  `form:"category_id"`
	// one speaker, partly typed ("andoseh")
	Speaker string `form:"speaker"`
	// one year ("2026")
	Year int32 `form:"year"`
	// one annual edition
	EditionID int64 `form:"edition_id"`
	// day 1, 2… of that edition (or of a category)
	Day int32 `form:"day"`
	// "2026-01-01"; messages recorded on or after
	From string `form:"from"`
	// "2026-12-31"; messages recorded on or before
	To string `form:"to"`
}

func parseDayFilter(value string) (sql.NullTime, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullTime{}, nil
	}
	day, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return sql.NullTime{}, err
	}
	return sql.NullTime{Time: day, Valid: true}, nil
}

// teachingCategoryFilter finds the category named by an old app (its name)
// or by a new one (its id or slug). It answers 0 when there is none.
func (s *Server) teachingCategoryFilter(c *gin.Context, request teachingsRequest) (int64, error) {
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
	category, err := s.store.GetTeachingCategoryBySlug(c, value)
	if err == nil {
		return category.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	category, err = s.store.GetTeachingCategoryByName(c, value)
	if err == nil {
		return category.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	// "camp des jeunes" written by hand, or an old category name
	categories, err := s.store.ListTeachingCategories(c, db.ListTeachingCategoriesParams{WithDrafts: true, WithArchived: true})
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

// GET /v1/teachings?search=&category=&speaker=&year=&day=&page=&size=: the
// published teachings, the newest message first.
func (s *Server) listTeachings(c *gin.Context) {
	s.teachings(c, false)
}

// GET /v1/admin/teachings?search=&category=&speaker=&year=&day=&page=&size=:
// the drafts too (after the published ones).
func (s *Server) adminListTeachings(c *gin.Context) {
	s.teachings(c, true)
}

func (s *Server) teachings(c *gin.Context, withDrafts bool) {
	var req teachingsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		badRequestError(c, err)
		return
	}
	search := req.Q
	if strings.TrimSpace(search) == "" {
		search = req.Search
	}
	from, err := parseDayFilter(req.From)
	if err != nil {
		badRequestError(c, errors.New("from: a day such as 2026-01-01"))
		return
	}
	to, err := parseDayFilter(req.To)
	if err != nil {
		badRequestError(c, errors.New("to: a day such as 2026-12-31"))
		return
	}
	categoryID, err := s.teachingCategoryFilter(c, req)
	if err != nil {
		internalServerError(c, err)
		return
	}
	limit, offset := req.limits()
	// a category that doesn't exist answers nothing, not the whole list
	if req.Category != "" && categoryID == 0 {
		paginatedApiResponse(c, http.StatusOK, []teachingResponse{}, MetaResponse{
			Total: 0, Limit: limit, Offset: offset,
		})
		return
	}
	params := db.CountTeachingsFilteredParams{
		WithDrafts: withDrafts,
		CategoryID: categoryID,
		EditionID:  req.EditionID,
		Year:       req.Year,
		Day:        req.Day,
		FromDate:   from,
		ToDate:     to,
		Speaker:    fold(req.Speaker),
		Search:     fold(search),
	}
	total, err := s.store.CountTeachingsFiltered(c, params)
	if err != nil {
		internalServerError(c, err)
		return
	}
	rows, err := s.store.ListTeachingsFiltered(c, db.ListTeachingsFilteredParams{
		WithDrafts: params.WithDrafts,
		CategoryID: params.CategoryID,
		EditionID:  params.EditionID,
		Year:       params.Year,
		Day:        params.Day,
		FromDate:   params.FromDate,
		ToDate:     params.ToDate,
		Speaker:    params.Speaker,
		Search:     params.Search,
		Lim:        limit,
		Off:        offset,
	})
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
	teachings := make([]teachingResponse, 0, len(rows))
	for _, row := range rows {
		teachings = append(teachings, toTeaching(row, covers))
	}
	paginatedApiResponse(c, http.StatusOK, teachings, MetaResponse{
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

type teachingCategoryResponse struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Count int    `json:"count"`
}

// GET /v1/teachings/categories: the categories of the published teachings,
// with how many each, in the order chosen by the admins (the filter chips).
func (s *Server) listTeachingCategories(c *gin.Context) {
	rows, err := s.store.ListTeachingCategoryCounts(c, false)
	if err != nil {
		internalServerError(c, err)
		return
	}
	categories := make([]teachingCategoryResponse, 0, len(rows))
	for _, row := range rows {
		categories = append(categories, teachingCategoryResponse{
			ID: row.ID, Name: row.Name, Slug: row.Slug, Count: int(row.Count),
		})
	}
	apiResponse(c, http.StatusOK, categories)
}

type teachingSpeakerFacet struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type teachingYearFacet struct {
	Year  int32 `json:"year"`
	Count int   `json:"count"`
}

type teachingDayFacet struct {
	Day   int32 `json:"day"`
	Count int   `json:"count"`
}

type teachingEditionFacet struct {
	ID           int64  `json:"id"`
	CategoryID   int64  `json:"category_id"`
	CategoryName string `json:"category_name"`
	CategorySlug string `json:"category_slug"`
	Year         int32  `json:"year"`
	Title        string `json:"title"`
	Count        int    `json:"count"`
}

type teachingFacetsResponse struct {
	Categories []teachingCategoryResponse `json:"categories"`
	Speakers   []teachingSpeakerFacet     `json:"speakers"`
	Years      []teachingYearFacet        `json:"years"`
	Editions   []teachingEditionFacet     `json:"editions"`
	Days       []teachingDayFacet         `json:"days,omitempty"`
}

// GET /v1/teachings/facets?edition_id=: the values and counts of the filters,
// for the chips and the advanced panel.
func (s *Server) listTeachingFacets(c *gin.Context) {
	var req struct {
		EditionID int64 `form:"edition_id"`
	}
	if err := c.ShouldBindQuery(&req); err != nil {
		badRequestError(c, err)
		return
	}
	categories, err := s.store.ListTeachingCategoryCounts(c, false)
	if err != nil {
		internalServerError(c, err)
		return
	}
	speakers, err := s.store.ListTeachingSpeakers(c, false)
	if err != nil {
		internalServerError(c, err)
		return
	}
	years, err := s.store.ListTeachingYears(c, false)
	if err != nil {
		internalServerError(c, err)
		return
	}
	editions, err := s.store.ListTeachingEditions(c, false)
	if err != nil {
		internalServerError(c, err)
		return
	}
	response := teachingFacetsResponse{
		Categories: make([]teachingCategoryResponse, 0, len(categories)),
		Speakers:   make([]teachingSpeakerFacet, 0, len(speakers)),
		Years:      make([]teachingYearFacet, 0, len(years)),
		Editions:   make([]teachingEditionFacet, 0, len(editions)),
	}
	for _, row := range categories {
		response.Categories = append(response.Categories, teachingCategoryResponse{
			ID: row.ID, Name: row.Name, Slug: row.Slug, Count: int(row.Count),
		})
	}
	for _, row := range speakers {
		response.Speakers = append(response.Speakers, teachingSpeakerFacet{Name: row.Speaker, Count: int(row.Count)})
	}
	for _, row := range years {
		response.Years = append(response.Years, teachingYearFacet{Year: int32(row.Year.Int16), Count: int(row.Count)})
	}
	for _, row := range editions {
		response.Editions = append(response.Editions, teachingEditionFacet{
			ID: row.ID, CategoryID: row.CategoryID, CategoryName: row.CategoryName,
			CategorySlug: row.CategorySlug, Year: int32(row.Year), Title: row.Title,
			Count: int(row.TeachingCount),
		})
	}
	if req.EditionID != 0 {
		days, err := s.store.ListTeachingDays(c, db.ListTeachingDaysParams{
			EditionID:  sql.NullInt64{Int64: req.EditionID, Valid: req.EditionID != 0},
			WithDrafts: false,
		})
		if err != nil {
			internalServerError(c, err)
			return
		}
		response.Days = make([]teachingDayFacet, 0, len(days))
		for _, row := range days {
			response.Days = append(response.Days, teachingDayFacet{Day: int32(row.DayNumber.Int16), Count: int(row.Count)})
		}
	}
	apiResponse(c, http.StatusOK, response)
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
	// the category's name; an old app sends only this
	Category string `json:"category"`
	// the category chosen in the new form
	CategoryID *int64 `json:"category_id"`
	// an annual edition ("Camp des jeunes 2026"), and its day
	EditionID *int64 `json:"edition_id"`
	Year      *int32 `json:"year"`
	DayNumber *int32 `json:"day_number"`
	Position  int32  `json:"position"`
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

type checkedTeachingValues struct {
	RecordedOn   sql.NullTime
	Cover        sql.NullInt64
	CategoryID   sql.NullInt64
	EditionID    sql.NullInt64
	Year         sql.NullInt16
	DayNumber    sql.NullInt16
	Position     int32
	CategoryName string
}

// checkedTeaching: trimmed, the lengths, the sound an MP3 or an M4A, the
// category and the edition found or created; the values to save.
func (s *Server) checkedTeaching(c *gin.Context, req *saveTeachingRequest) (checkedTeachingValues, error) {
	var values checkedTeachingValues
	req.Title = strings.TrimSpace(req.Title)
	req.Speaker = strings.Join(strings.Fields(req.Speaker), " ")
	req.Description = strings.TrimSpace(req.Description)
	req.Category = strings.Join(strings.Fields(req.Category), " ")
	switch {
	case req.Title == "":
		return values, errors.New("title is required")
	case utf8.RuneCountInString(req.Title) > 200:
		return values, errors.New("title: 200 characters at most")
	case utf8.RuneCountInString(req.Speaker) > 120:
		return values, errors.New("speaker: 120 characters at most")
	case utf8.RuneCountInString(req.Description) > 4000:
		return values, errors.New("description: 4000 characters at most")
	case utf8.RuneCountInString(req.Category) > 120:
		return values, errors.New("category: 120 characters at most")
	case req.DurationSeconds < 0 || req.DurationSeconds > 24*3600:
		return values, errors.New("duration_seconds: 0 to 86400")
	case req.AudioID == 0:
		return values, errors.New("audio_id is required")
	case req.Position < 0 || req.Position > 10000:
		return values, errors.New("position: 0 to 10000")
	}
	if req.RecordedOn != nil && strings.TrimSpace(*req.RecordedOn) != "" {
		day, err := time.Parse(time.DateOnly, strings.TrimSpace(*req.RecordedOn))
		if err != nil || day.Year() < 1900 || day.After(time.Now().AddDate(0, 0, 1)) {
			return values, errors.New("recorded_on: a day such as 2024-08-15, not in the future")
		}
		values.RecordedOn = sql.NullTime{Time: day, Valid: true}
	}

	// the category: an id, a name already known, or a new one typed by hand
	var category db.TeachingCategory
	if req.CategoryID != nil && *req.CategoryID != 0 {
		row, err := s.store.GetTeachingCategory(c, *req.CategoryID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return values, errors.New("category_id: unknown category")
			}
			return values, err
		}
		category = row
	} else if req.Category != "" {
		row, err := s.store.GetTeachingCategoryByName(c, req.Category)
		if errors.Is(err, sql.ErrNoRows) {
			row, err = s.createTeachingCategoryFromName(c, req.Category)
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

	// the annual edition, if chosen
	if req.EditionID != nil && *req.EditionID != 0 {
		edition, err := s.store.GetTeachingEdition(c, *req.EditionID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return values, errors.New("edition_id: unknown edition")
			}
			return values, err
		}
		if category.ID != 0 && edition.CategoryID != category.ID {
			return values, errors.New("edition_id: this edition belongs to another category")
		}
		if category.ID == 0 {
			row, err := s.store.GetTeachingCategory(c, edition.CategoryID)
			if err != nil {
				return values, err
			}
			category = row
			values.CategoryID = sql.NullInt64{Int64: category.ID, Valid: true}
			req.Category = category.Name
		}
		values.EditionID = sql.NullInt64{Int64: edition.ID, Valid: true}
		values.Year = sql.NullInt16{Int16: edition.Year, Valid: true}
	}

	// the year: given, or taken from the edition, or from the day recorded
	if req.Year != nil && *req.Year != 0 {
		if *req.Year < 1900 || *req.Year > 2200 {
			return values, errors.New("year: 1900 to 2200")
		}
		values.Year = sql.NullInt16{Int16: int16(*req.Year), Valid: true}
	} else if !values.Year.Valid && values.RecordedOn.Valid {
		values.Year = sql.NullInt16{Int16: int16(values.RecordedOn.Time.Year()), Valid: true}
	}

	// the day of a camp or a crusade: several teachings may share it
	if req.DayNumber != nil && *req.DayNumber != 0 {
		if *req.DayNumber < 1 || *req.DayNumber > 366 {
			return values, errors.New("day_number: 1 to 366")
		}
		values.DayNumber = sql.NullInt16{Int16: int16(*req.DayNumber), Valid: true}
	}
	values.Position = req.Position

	ids := []int64{req.AudioID}
	if req.CoverID != nil && *req.CoverID != 0 {
		ids = append(ids, *req.CoverID)
	}
	rows, err := s.store.ListMediaByIDs(c, ids)
	if err != nil {
		return values, err
	}
	types := map[int64]string{}
	for _, row := range rows {
		types[row.ID] = row.ContentType
	}
	if !(media.Info{ContentType: types[req.AudioID]}).IsAudio() {
		return values, fmt.Errorf("audio_id %d is not an MP3 nor an M4A", req.AudioID)
	}
	if len(ids) == 2 {
		if !strings.HasPrefix(types[ids[1]], "image/") {
			return values, fmt.Errorf("cover_id %d is not an image", ids[1])
		}
		values.Cover = sql.NullInt64{Int64: ids[1], Valid: true}
	}
	return values, nil
}

// teachingByID answers one teaching with its cover.
func (s *Server) teachingByID(c *gin.Context, status int, id int64) {
	row, err := s.store.GetTeaching(c, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "teaching")
			return
		}
		internalServerError(c, err)
		return
	}
	covers, err := s.mediaByIDs(c, []int64{row.CoverID.Int64})
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, status, toTeaching(row, covers))
}

// POST /v1/admin/teachings
func (s *Server) createTeaching(c *gin.Context) {
	var req saveTeachingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	values, err := s.checkedTeaching(c, &req)
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
		CategoryID:      values.CategoryID,
		EditionID:       values.EditionID,
		Year:            values.Year,
		DayNumber:       values.DayNumber,
		Position:        values.Position,
		RecordedOn:      values.RecordedOn,
		AudioID:         req.AudioID,
		DurationSeconds: req.DurationSeconds,
		CoverID:         values.Cover,
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
	values, err := s.checkedTeaching(c, &req)
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
		CategoryID:      values.CategoryID,
		EditionID:       values.EditionID,
		Year:            values.Year,
		DayNumber:       values.DayNumber,
		Position:        values.Position,
		RecordedOn:      values.RecordedOn,
		AudioID:         req.AudioID,
		DurationSeconds: req.DurationSeconds,
		CoverID:         values.Cover,
		Published:       req.Published,
	}); err != nil {
		internalServerError(c, err)
		return
	}
	if before.AudioID != req.AudioID {
		s.dropTeachingMedia(c, before.AudioID)
	}
	if before.CoverID.Valid && before.CoverID != values.Cover {
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
