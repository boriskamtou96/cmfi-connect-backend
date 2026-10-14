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
)

// ----- our ministry: the nations where it is present -----

type nationResponse struct {
	ID       int64 `json:"id"`
	Position int32 `json:"position"`
	// as the list writes it ("Le Cameroun"); the apps translate a country by its code
	Name string `json:"name"`
	// ISO code ("CM"), "" for a region that is not a country (Écosse…)
	CountryCode string    `json:"country_code"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	ArrivalYear *int32    `json:"arrival_year"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toNationResponse(nation db.MinistryNation) nationResponse {
	response := nationResponse{
		ID:          nation.ID,
		Position:    nation.Position,
		Name:        nation.Name,
		CountryCode: nation.CountryCode,
		Latitude:    nation.Latitude,
		Longitude:   nation.Longitude,
		UpdatedAt:   nation.UpdatedAt,
	}
	if nation.ArrivalYear.Valid {
		response.ArrivalYear = &nation.ArrivalYear.Int32
	}
	return response
}

// GET /v1/ministry/nations: the nations, in the order the ministry reached them.
func (s *Server) listNations(c *gin.Context) {
	nations, err := s.store.ListNations(c)
	if err != nil {
		internalServerError(c)
		return
	}
	responses := make([]nationResponse, len(nations))
	for i, nation := range nations {
		responses[i] = toNationResponse(nation)
	}
	apiResponse(c, http.StatusOK, responses)
}

type nationRequest struct {
	Name        string   `json:"name"`
	CountryCode string   `json:"country_code"`
	Latitude    *float64 `json:"latitude"`
	Longitude   *float64 `json:"longitude"`
	ArrivalYear *int32   `json:"arrival_year"`
	// its number in the list; 0 or none: at the end (or unchanged)
	Position int32 `json:"position"`
}

var isoCountry = regexp.MustCompile(`^[A-Z]{2}$`)

func (r nationRequest) check() (db.CreateNationParams, error) {
	name := strings.TrimSpace(r.Name)
	code := strings.ToUpper(strings.TrimSpace(r.CountryCode))
	switch {
	case name == "":
		return db.CreateNationParams{}, errors.New("name is required")
	case utf8.RuneCountInString(name) > 120:
		return db.CreateNationParams{}, errors.New("name: 120 characters at most")
	case code != "" && !isoCountry.MatchString(code):
		return db.CreateNationParams{}, errors.New("country_code: an ISO code of 2 letters, or empty")
	case r.Latitude == nil || *r.Latitude < -90 || *r.Latitude > 90:
		return db.CreateNationParams{}, errors.New("latitude: from -90 to 90")
	case r.Longitude == nil || *r.Longitude < -180 || *r.Longitude > 180:
		return db.CreateNationParams{}, errors.New("longitude: from -180 to 180")
	case r.ArrivalYear != nil && (*r.ArrivalYear < 1900 || *r.ArrivalYear > 2200):
		return db.CreateNationParams{}, fmt.Errorf("arrival_year: from 1900 to 2200")
	}
	params := db.CreateNationParams{
		Position:    r.Position,
		Name:        name,
		CountryCode: code,
		Latitude:    *r.Latitude,
		Longitude:   *r.Longitude,
	}
	if r.ArrivalYear != nil {
		params.ArrivalYear = sql.NullInt32{Int32: *r.ArrivalYear, Valid: true}
	}
	return params, nil
}

// POST /v1/admin/ministry/nations: a new nation, at the end of the list or
// at [position] (the following ones move down).
func (s *Server) createNation(c *gin.Context) {
	var req nationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	params, err := req.check()
	if err != nil {
		badRequestError(c, err)
		return
	}
	nation, err := s.store.CreateNationTx(c, params)
	if err != nil {
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusCreated, toNationResponse(nation))
}

// PUT /v1/admin/ministry/nations/:id: everything about a nation; a new
// [position] moves it in the list (0 keeps its place).
func (s *Server) updateNation(c *gin.Context) {
	var uri eventIDRequest
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req nationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	params, err := req.check()
	if err != nil {
		badRequestError(c, err)
		return
	}
	if params.Position == 0 {
		current, err := s.store.GetNation(c, uri.ID)
		if err != nil {
			s.nationError(c, err)
			return
		}
		params.Position = current.Position
	}
	nation, err := s.store.UpdateNationTx(c, db.UpdateNationParams{
		ID:          uri.ID,
		Position:    params.Position,
		Name:        params.Name,
		CountryCode: params.CountryCode,
		Latitude:    params.Latitude,
		Longitude:   params.Longitude,
		ArrivalYear: params.ArrivalYear,
	})
	if err != nil {
		s.nationError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, toNationResponse(nation))
}

// DELETE /v1/admin/ministry/nations/:id: the following nations move up.
func (s *Server) deleteNation(c *gin.Context) {
	var uri eventIDRequest
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	if err := s.store.DeleteNationTx(c, uri.ID); err != nil {
		s.nationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) nationError(c *gin.Context, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "Nation")
		return
	}
	internalServerError(c)
}
