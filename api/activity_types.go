package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

var activityCodePattern = regexp.MustCompile(`^[A-Z0-9_]+$`)

type activityTypeResponse struct {
	ID             int64     `json:"id"`
	Code           string    `json:"code"`
	Label          string    `json:"label"`
	TracksQuantity bool      `json:"tracks_quantity"`
	QuantityUnit   *string   `json:"quantity_unit"`
	TracksDuration bool      `json:"tracks_duration"`
	Position       int32     `json:"position"`
	IsStandard     bool      `json:"is_standard"`
	CreatedAt      time.Time `json:"created_at"`
}

func (s *Server) listActivityTypes(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	activityTypes, err := s.store.GetUserActivityTypes(c, authUser.ID)
	if err != nil {
		internalServerError(c)
		return
	}

	response := make([]activityTypeResponse, 0, len(activityTypes))
	for _, activityType := range activityTypes {
		response = append(response, convertActivityTypeResponse(activityType))
	}

	apiResponse(c, http.StatusOK, response)
}

type createActivityTypePayload struct {
	Code           string `json:"code" binding:"required,max=20"`
	Label          string `json:"label" binding:"required,max=100"`
	TracksQuantity bool   `json:"tracks_quantity"`
	QuantityUnit   string `json:"quantity_unit" binding:"max=30"`
	TracksDuration bool   `json:"tracks_duration"`
}

func (s *Server) createActivityType(c *gin.Context) {
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

	if !s.checkStandardCodeIsFree(c, code) {
		return
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	params := db.CreateActivityTypeParams{
		UserID:         authUser.ID,
		Code:           code,
		Label:          label,
		TracksQuantity: payload.TracksQuantity,
		QuantityUnit: sql.NullString{
			String: unit,
			Valid:  unit != "",
		},
		TracksDuration: payload.TracksDuration,
	}

	activityType, err := s.store.CreateActivityType(c, params)
	if err != nil {
		if pgErr, ok := errors.AsType[*pq.Error](err); ok && pgErr.Code.Name() == "unique_violation" {
			// archived items keep their code reserved
			conflictError(c, fmt.Errorf("you already have an item with code %s (it may be archived)", code))
			return
		}
		internalServerError(c)
		return
	}

	apiResponse(c, http.StatusCreated, convertActivityTypeResponse(activityType))
}

type activityTypeURI struct {
	ID int64 `uri:"id" binding:"required,min=1"`
}

// tracks_quantity and tracks_duration are not editable: past entries were saved with them.
type updateActivityTypePayload struct {
	Code         string `json:"code" binding:"required,max=20"`
	Label        string `json:"label" binding:"required,max=100"`
	QuantityUnit string `json:"quantity_unit" binding:"max=30"`
	Position     *int32 `json:"position" binding:"omitempty,min=101,max=10000"`
}

func (s *Server) updateActivityType(c *gin.Context) {
	var uri activityTypeURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}

	var payload updateActivityTypePayload
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

	if !s.checkStandardCodeIsFree(c, code) {
		return
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	params := db.UpdateActivityTypeParams{
		ID:     uri.ID,
		UserID: authUser.ID,
		Code:   code,
		Label:  label,
		QuantityUnit: sql.NullString{
			String: unit,
			Valid:  unit != "",
		},
	}
	if payload.Position != nil {
		params.Position = sql.NullInt32{
			Int32: *payload.Position,
			Valid: true,
		}
	}

	activityType, err := s.store.UpdateActivityType(c, params)
	if err != nil {
		// no row: unknown id, archived item, someone else's item or a standard item
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Activity type")
			return
		}
		if pgErr, ok := errors.AsType[*pq.Error](err); ok {
			switch pgErr.Code.Name() {
			case "unique_violation":
				conflictError(c, fmt.Errorf("you already have an item with code %s (it may be archived)", code))
				return
			case "check_violation":
				badRequestError(c, errors.New("quantity_unit is only allowed for an item that tracks a quantity"))
				return
			}
		}
		internalServerError(c)
		return
	}

	apiResponse(c, http.StatusOK, convertActivityTypeResponse(activityType))
}

// archiveActivityType hides the item from the forms; its past values stay in the reports.
func (s *Server) archiveActivityType(c *gin.Context) {
	var uri activityTypeURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	rows, err := s.store.ArchiveActivityType(c, db.ArchiveActivityTypeParams{
		ID:     uri.ID,
		UserID: authUser.ID,
	})
	if err != nil {
		internalServerError(c)
		return
	}
	if rows == 0 {
		resourceNotFoundError(c, "Activity type")
		return
	}

	apiResponse(c, http.StatusNoContent, nil)
}

func validateActivityCodeAndLabel(code, label string) error {
	if !activityCodePattern.MatchString(code) {
		return errors.New("code must only contain letters, digits or _")
	}
	if label == "" {
		return errors.New("label is required")
	}
	return nil
}

// checkStandardCodeIsFree answers 409 when the code belongs to a standard item (LB, PS...).
// The unique constraint can't catch it: (NULL, 'LB') and (12, 'LB') are different pairs.
func (s *Server) checkStandardCodeIsFree(c *gin.Context, code string) bool {
	isStandard, err := s.store.IsStandardActivityCode(c, code)
	if err != nil {
		internalServerError(c)
		return false
	}
	if isStandard {
		conflictError(c, fmt.Errorf("code %s is reserved for a standard item", code))
		return false
	}
	return true
}

func convertActivityTypeResponse(activityType db.ActivityType) activityTypeResponse {
	var quantityUnitPtr *string

	if activityType.QuantityUnit.Valid {
		quantityUnitPtr = &activityType.QuantityUnit.String
	}

	return activityTypeResponse{
		ID:             activityType.ID,
		Code:           activityType.Code,
		Label:          activityType.Label,
		TracksQuantity: activityType.TracksQuantity,
		QuantityUnit:   quantityUnitPtr,
		TracksDuration: activityType.TracksDuration,
		Position:       activityType.Position,
		IsStandard:     !activityType.UserID.Valid,
		CreatedAt:      activityType.CreatedAt,
	}
}
