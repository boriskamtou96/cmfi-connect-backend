package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
)

// Our missionaries and our heroes (Notre ministère): the admins write them,
// every member reads the published ones.

const (
	kindMissionary = "missionary"
	kindHero       = "hero"
)

type ministryPersonResponse struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Photo: null without one.
	Photo       *mediaResponse `json:"photo"`
	CountryCode string         `json:"country_code"`
	Role        string         `json:"role"`
	SinceYear   *int32         `json:"since_year"`
	Years       string         `json:"years"`
	Story       string         `json:"story"`
	Position    int32          `json:"position"`
	// Published: the drafts are only seen by the admins.
	Published bool `json:"published"`
}

type ministryPeopleQuery struct {
	Kind string `form:"kind" binding:"required,oneof=missionary hero"`
}

// GET /v1/ministry/people?kind=missionary|hero: the published ones.
func (s *Server) listMinistryPeople(c *gin.Context) {
	s.ministryPeople(c, false)
}

// GET /v1/admin/ministry/people?kind=missionary|hero: the drafts too.
func (s *Server) adminListMinistryPeople(c *gin.Context) {
	s.ministryPeople(c, true)
}

func (s *Server) ministryPeople(c *gin.Context, withDrafts bool) {
	var query ministryPeopleQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		badRequestError(c, err)
		return
	}
	people, err := s.store.ListMinistryPeople(c, db.ListMinistryPeopleParams{
		Kind:       query.Kind,
		WithDrafts: withDrafts,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	responses, err := s.ministryPersonResponses(c, people)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, responses)
}

type ministryPersonRequest struct {
	// Kind is read on creation only.
	Kind        string `json:"kind"`
	Name        string `json:"name" binding:"required"`
	PhotoID     *int64 `json:"photo_id"`
	CountryCode string `json:"country_code"`
	Role        string `json:"role"`
	SinceYear   *int32 `json:"since_year"`
	Years       string `json:"years"`
	Story       string `json:"story"`
	Published   bool   `json:"published"`
	// Position: its place among the others (update only).
	Position *int32 `json:"position" binding:"omitempty,min=0"`
}

// check trims the request and says what is wrong in it, if anything.
func (r *ministryPersonRequest) check() error {
	r.Name = strings.TrimSpace(r.Name)
	r.CountryCode = strings.ToUpper(strings.TrimSpace(r.CountryCode))
	r.Role = strings.TrimSpace(r.Role)
	r.Years = strings.TrimSpace(r.Years)
	r.Story = strings.TrimSpace(r.Story)
	switch {
	case r.Name == "" || utf8.RuneCountInString(r.Name) > 150:
		return errors.New("name: 1 to 150 characters")
	case r.CountryCode != "" && !countryCode.MatchString(r.CountryCode):
		return errors.New("country_code: an ISO code such as CM")
	case utf8.RuneCountInString(r.Role) > 200:
		return errors.New("role: 200 characters at most")
	case utf8.RuneCountInString(r.Years) > 40:
		return errors.New("years: 40 characters at most")
	case utf8.RuneCountInString(r.Story) > 20000:
		return errors.New("story: 20 000 characters at most")
	case r.SinceYear != nil && (*r.SinceYear < 1900 || *r.SinceYear > 2100):
		return errors.New("since_year: 1900 to 2100")
	}
	return nil
}

// photoOf checks that [id] is an image (not a PDF); 0 or nil: no photo.
func (s *Server) photoOf(c *gin.Context, id *int64) (sql.NullInt64, error) {
	if id == nil || *id == 0 {
		return sql.NullInt64{}, nil
	}
	rows, err := s.store.ListMediaByIDs(c, []int64{*id})
	if err != nil {
		return sql.NullInt64{}, err
	}
	if len(rows) != 1 || rows[0].ContentType == "application/pdf" {
		return sql.NullInt64{}, fmt.Errorf("photo_id %d is not an image", *id)
	}
	return sql.NullInt64{Int64: *id, Valid: true}, nil
}

// POST /v1/admin/ministry/people
func (s *Server) createMinistryPerson(c *gin.Context) {
	var req ministryPersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if req.Kind != kindMissionary && req.Kind != kindHero {
		badRequestError(c, errors.New("kind: missionary or hero"))
		return
	}
	if err := req.check(); err != nil {
		badRequestError(c, err)
		return
	}
	photo, err := s.photoOf(c, req.PhotoID)
	if err != nil {
		badRequestError(c, err)
		return
	}
	person, err := s.store.CreateMinistryPerson(c, db.CreateMinistryPersonParams{
		Kind:        req.Kind,
		Name:        req.Name,
		PhotoID:     photo,
		CountryCode: req.CountryCode,
		Role:        req.Role,
		SinceYear:   nullInt32(req.SinceYear),
		Years:       req.Years,
		Story:       req.Story,
		Published:   req.Published,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	s.respondMinistryPerson(c, http.StatusCreated, person)
}

type ministryPersonURI struct {
	ID int64 `uri:"id" binding:"required,min=1"`
}

// PUT /v1/admin/ministry/people/:id
func (s *Server) updateMinistryPerson(c *gin.Context) {
	var uri ministryPersonURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req ministryPersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if err := req.check(); err != nil {
		badRequestError(c, err)
		return
	}
	before, err := s.store.GetMinistryPerson(c, uri.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Person")
			return
		}
		internalServerError(c, err)
		return
	}
	photo, err := s.photoOf(c, req.PhotoID)
	if err != nil {
		badRequestError(c, err)
		return
	}
	params := db.UpdateMinistryPersonParams{
		Name:        req.Name,
		PhotoID:     photo,
		CountryCode: req.CountryCode,
		Role:        req.Role,
		SinceYear:   nullInt32(req.SinceYear),
		Years:       req.Years,
		Story:       req.Story,
		Published:   req.Published,
		ID:          uri.ID,
	}
	if req.Position != nil {
		params.Position = sql.NullInt32{Int32: *req.Position, Valid: true}
	}
	person, err := s.store.UpdateMinistryPerson(c, params)
	if err != nil {
		internalServerError(c, err)
		return
	}
	// a photo replaced or removed: its file goes
	if before.PhotoID.Valid && before.PhotoID != person.PhotoID {
		s.dropMedia(c, before.PhotoID.Int64)
	}
	s.respondMinistryPerson(c, http.StatusOK, person)
}

// DELETE /v1/admin/ministry/people/:id
func (s *Server) deleteMinistryPerson(c *gin.Context) {
	var uri ministryPersonURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	photo, err := s.store.DeleteMinistryPerson(c, uri.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Person")
			return
		}
		internalServerError(c, err)
		return
	}
	if photo.Valid {
		s.dropMedia(c, photo.Int64)
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) respondMinistryPerson(c *gin.Context, status int, person db.MinistryPerson) {
	responses, err := s.ministryPersonResponses(c, []db.MinistryPerson{person})
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, status, responses[0])
}

func (s *Server) ministryPersonResponses(c *gin.Context, people []db.MinistryPerson) ([]ministryPersonResponse, error) {
	ids := make([]int64, 0, len(people))
	for _, person := range people {
		ids = append(ids, person.PhotoID.Int64)
	}
	photos, err := s.mediaByIDs(c, ids)
	if err != nil {
		return nil, err
	}
	responses := make([]ministryPersonResponse, 0, len(people))
	for _, person := range people {
		response := ministryPersonResponse{
			ID:          person.ID,
			Kind:        person.Kind,
			Name:        person.Name,
			CountryCode: person.CountryCode,
			Role:        person.Role,
			Years:       person.Years,
			Story:       person.Story,
			Position:    person.Position,
			Published:   person.Published,
		}
		if photo, ok := photos[person.PhotoID.Int64]; ok && person.PhotoID.Valid {
			response.Photo = &photo
		}
		if person.SinceYear.Valid {
			year := person.SinceYear.Int32
			response.SinceYear = &year
		}
		responses = append(responses, response)
	}
	return responses, nil
}
