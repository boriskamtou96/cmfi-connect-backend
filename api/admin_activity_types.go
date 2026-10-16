package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

// The standard items of the report (Lecture biblique, Prière seule…), the
// same for every member, kept by the admins: their label, unit and order,
// new ones, archived ones. The code and the measures never change once made:
// the reports and the app rely on them.

// lockedStandardCodes can't be archived: the app counts the Bible chapters
// read in LB, the retreats in PS.
var lockedStandardCodes = []string{"LB", "PS"}

type adminActivityTypeResponse struct {
	activityTypeResponse
	Archived bool `json:"archived"`
	// Locked: can't be archived (the app relies on it).
	Locked bool `json:"locked"`
}

func newAdminActivityTypeResponse(item db.ActivityType) adminActivityTypeResponse {
	return adminActivityTypeResponse{
		activityTypeResponse: convertActivityTypeResponse(item),
		Archived:             item.ArchivedAt.Valid,
		Locked:               slices.Contains(lockedStandardCodes, item.Code),
	}
}

// GET /v1/admin/activity-types
func (s *Server) adminListActivityTypes(c *gin.Context) {
	items, err := s.store.ListStandardActivityTypes(c)
	if err != nil {
		internalServerError(c, err)
		return
	}
	response := make([]adminActivityTypeResponse, 0, len(items))
	for _, item := range items {
		response = append(response, newAdminActivityTypeResponse(item))
	}
	apiResponse(c, http.StatusOK, response)
}

// POST /v1/admin/activity-types: a new standard item, for every member.
func (s *Server) adminCreateActivityType(c *gin.Context) {
	var payload createActivityTypePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequestError(c, err)
		return
	}
	code := strings.ToUpper(strings.TrimSpace(payload.Code))
	label := strings.TrimSpace(payload.Label)
	unit := strings.TrimSpace(payload.QuantityUnit)
	if err := validateActivityCodeAndLabel(code, label); err != nil {
		badRequestError(c, err)
		return
	}
	if !payload.TracksQuantity && !payload.TracksDuration {
		badRequestError(c, errors.New("choose at least one measure: tracks_quantity or tracks_duration"))
		return
	}
	if unit != "" && !payload.TracksQuantity {
		badRequestError(c, errors.New("quantity_unit is only allowed when tracks_quantity is true"))
		return
	}
	// a member's own item with this code would show twice in their report
	taken, err := s.store.CountPersonalItemsWithCode(c, code)
	if err != nil {
		internalServerError(c, err)
		return
	}
	if taken > 0 {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "CODE_USED_BY_MEMBERS",
			fmt.Errorf("%d member(s) already have a personal item with code %s", taken, code)))
		return
	}

	item, err := s.store.CreateStandardActivityType(c, db.CreateStandardActivityTypeParams{
		Code:           code,
		Label:          label,
		TracksQuantity: payload.TracksQuantity,
		QuantityUnit:   sql.NullString{String: unit, Valid: unit != ""},
		TracksDuration: payload.TracksDuration,
	})
	if err != nil {
		if pgErr, ok := errors.AsType[*pq.Error](err); ok && pgErr.Code.Name() == "unique_violation" {
			conflictError(c, fmt.Errorf("a standard item with code %s already exists (it may be archived)", code))
			return
		}
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusCreated, newAdminActivityTypeResponse(item))
}

type adminUpdateActivityTypePayload struct {
	Label        string `json:"label" binding:"required,max=100"`
	QuantityUnit string `json:"quantity_unit" binding:"max=30"`
	// Position: 1 to 99 (the personal items come after).
	Position *int32 `json:"position" binding:"omitempty,min=1,max=99"`
}

// PUT /v1/admin/activity-types/:id: its label, unit or place.
func (s *Server) adminUpdateActivityType(c *gin.Context) {
	var uri activityTypeURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var payload adminUpdateActivityTypePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequestError(c, err)
		return
	}
	label := strings.TrimSpace(payload.Label)
	unit := strings.TrimSpace(payload.QuantityUnit)
	if label == "" {
		badRequestError(c, errors.New("label is required"))
		return
	}
	params := db.UpdateStandardActivityTypeParams{
		Label:        label,
		QuantityUnit: sql.NullString{String: unit, Valid: unit != ""},
		ID:           uri.ID,
	}
	if payload.Position != nil {
		params.Position = sql.NullInt32{Int32: *payload.Position, Valid: true}
	}
	item, err := s.store.UpdateStandardActivityType(c, params)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Activity type")
			return
		}
		if pgErr, ok := errors.AsType[*pq.Error](err); ok && pgErr.Code.Name() == "check_violation" {
			badRequestError(c, errors.New("quantity_unit is only allowed on an item counted in quantity"))
			return
		}
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, newAdminActivityTypeResponse(item))
}

type archivePayload struct {
	Archived bool `json:"archived"`
}

// PUT /v1/admin/activity-types/:id/archive {"archived": true|false}
func (s *Server) adminArchiveActivityType(c *gin.Context) {
	var uri activityTypeURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var payload archivePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequestError(c, err)
		return
	}
	if payload.Archived {
		items, err := s.store.ListStandardActivityTypes(c)
		if err != nil {
			internalServerError(c, err)
			return
		}
		for _, item := range items {
			if item.ID == uri.ID && slices.Contains(lockedStandardCodes, item.Code) {
				c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "ITEM_LOCKED",
					fmt.Errorf("%s can't be archived: the app relies on it", item.Code)))
				return
			}
		}
	}
	item, err := s.store.SetStandardActivityTypeArchived(c, db.SetStandardActivityTypeArchivedParams{
		Archived: payload.Archived,
		ID:       uri.ID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Activity type")
			return
		}
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, newAdminActivityTypeResponse(item))
}
