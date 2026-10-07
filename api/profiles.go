package api

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

type createProfileRequest struct {
	BirthDate *time.Time `json:"birth_date"`
	City      *string    `json:"city"`
	Country   *string    `json:"country"`
	Church    *string    `json:"church"`
	Assembly  *string    `json:"assembly"`
}

type profileResponse struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"user_id"`
	BirthDate *time.Time `json:"birth_date,omitempty"`
	City      *string    `json:"city,omitempty"`
	Country   *string    `json:"country,omitempty"`
	Church    *string    `json:"church,omitempty"`
	Assembly  *string    `json:"assembly,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (s *Server) createProfile(c *gin.Context) {
	var payload createProfileRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequestError(c, err)
		return
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	params := db.CreateProfileParams{
		UserID: authUser.ID,
	}

	if payload.BirthDate != nil {
		params.BirthDate = sql.NullTime{
			Time:  *payload.BirthDate,
			Valid: true,
		}
	}

	if payload.City != nil {
		params.City = sql.NullString{
			String: *payload.City,
			Valid:  true,
		}
	}

	if payload.Country != nil {
		params.Country = sql.NullString{
			String: *payload.Country,
			Valid:  true,
		}
	}

	if payload.Church != nil {
		params.Church = sql.NullString{
			String: *payload.Church,
			Valid:  true,
		}
	}

	if payload.Assembly != nil {
		params.Assembly = sql.NullString{
			String: *payload.Assembly,
			Valid:  true,
		}
	}

	profile, err := s.store.CreateProfile(c, params)
	if err != nil {
		internalServerError(c)
		return
	}

	apiResponse(c, http.StatusCreated, convertToProfileResponse(profile))
}

func (s *Server) getProfile(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	profile, err := s.store.GetProfile(c, authUser.ID)
	if err != nil {
		resourceNotFoundError(c, "Profile")
		return
	}

	if profile.UserID != authUser.ID {
		unauthorizedError(c)
		return
	}

	apiResponse(c, http.StatusOK, convertToProfileResponse(profile))
}

type updateProfileRequest struct {
	BirthDate *time.Time `json:"birth_date"`
	City      *string    `json:"city"`
	Country   *string    `json:"country"`
	Church    *string    `json:"church"`
	Assembly  *string    `json:"assembly"`
}

func (s *Server) updateProfile(c *gin.Context) {
	var payload updateProfileRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequestError(c, err)
		return
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	params := db.UpdateProfileParams{
		UserID: authUser.ID,
	}

	if payload.BirthDate != nil {
		params.BirthDate = sql.NullTime{
			Time:  *payload.BirthDate,
			Valid: true,
		}
	}

	if payload.City != nil {
		params.City = sql.NullString{
			String: *payload.City,
			Valid:  true,
		}
	}

	if payload.Country != nil {
		params.Country = sql.NullString{
			String: *payload.Country,
			Valid:  true,
		}
	}

	if payload.Church != nil {
		params.Church = sql.NullString{
			String: *payload.Church,
			Valid:  true,
		}
	}

	if payload.Assembly != nil {
		params.Assembly = sql.NullString{
			String: *payload.Assembly,
			Valid:  true,
		}
	}

	profile, err := s.store.UpdateProfile(c, params)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Profile")
			return
		}
		internalServerError(c)
		return
	}

	apiResponse(c, http.StatusOK, convertToProfileResponse(profile))
}

func convertToProfileResponse(profile db.Profile) profileResponse {
	var birthDatePtr *time.Time
	if profile.BirthDate.Valid {
		birthDatePtr = &profile.BirthDate.Time
	}

	var cityPtr, countryPtr, churchPtr, assemblyPtr *string

	if profile.City.Valid {
		cityPtr = &profile.City.String
	}
	if profile.Country.Valid {
		countryPtr = &profile.Country.String
	}
	if profile.Church.Valid {
		churchPtr = &profile.Church.String
	}
	if profile.Assembly.Valid {
		assemblyPtr = &profile.Assembly.String
	}

	return profileResponse{
		ID:        profile.ID,
		UserID:    profile.UserID,
		BirthDate: birthDatePtr,
		City:      cityPtr,
		Country:   countryPtr,
		Church:    churchPtr,
		Assembly:  assemblyPtr,
		CreatedAt: profile.CreatedAt,
		UpdatedAt: profile.UpdatedAt,
	}
}
