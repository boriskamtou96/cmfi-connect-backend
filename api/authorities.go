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

type authorityResponse struct {
	ID              int64     `json:"id"`
	UserID          int64     `json:"user_id"`
	FirstName       string    `json:"first_name"`
	LastName        *string   `json:"last_name"`
	PhoneNumber     string    `json:"phone_number"`
	Email           *string   `json:"email"`
	IsDiscipleMaker bool      `json:"is_disciple_maker"`
	CreatedAt       time.Time `json:"created_at"`
}

type createAuthorityPayload struct {
	FirstName       string `json:"first_name" binding:"required"`
	LastName        string `json:"last_name"`
	PhoneNumber     string `json:"phone_number" binding:"required"`
	Email           string `json:"email"`
	IsDiscipleMaker bool   `json:"is_disciple_maker"`
}

func (s *Server) createAuthority(c *gin.Context) {
	var payload createAuthorityPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequestError(c, err)
		return
	}

	authPayload := c.MustGet(authorizationPayloadKey).(*token.Payload)

	params := db.CreateUserAuthorityParams{
		FirstName: payload.FirstName,
		LastName: sql.NullString{
			String: payload.LastName,
			Valid:  payload.LastName != "",
		},
		PhoneNumber: payload.PhoneNumber,
		Email: sql.NullString{
			String: payload.Email,
			Valid:  payload.Email != "",
		},
		IsDiscipleMaker: payload.IsDiscipleMaker,
		UserID:          authPayload.ID,
	}

	authority, err := s.store.CreateUserAuthority(c, params)
	if err != nil {
		internalServerError(c)
		return
	}

	apiResponse(c, http.StatusCreated, convertAuthorityResponse(authority))
}

type getUserAuthoritiesParams struct {
	Page int32 `form:"page" binding:"required,min=1"`
	Size int32 `form:"size" binding:"required,min=5,max=10"`
}

func (s *Server) listAuthorities(c *gin.Context) {
	var req getUserAuthoritiesParams
	if err := c.ShouldBind(&req); err != nil {
		badRequestError(c, err)
		return
	}

	params := db.GetUserAuthoritiesParams{
		Limit:  req.Size,
		Offset: (req.Page - 1) * req.Size,
	}
	authorities, err := s.store.GetUserAuthorities(c, params)
	if err != nil {
		internalServerError(c)
		return
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	var userAuthorities = make([]authorityResponse, 0)

	for _, authority := range authorities {
		if authority.UserID == authUser.ID {
			userAuthorities = append(userAuthorities, convertAuthorityResponse(authority))
		}
	}

	total, err := s.store.CountUserAuthorities(c, authUser.ID)
	if err != nil {
		internalServerError(c)
		return
	}

	meta := MetaResponse{
		Offset: params.Offset,
		Limit:  params.Limit,
		Total:  total,
	}

	paginatedApiResponse(c, http.StatusOK, userAuthorities, meta)
}

type getAuthorityRequest struct {
	ID int64 `uri:"id" binding:"required"`
}

func (s *Server) getAuthorityById(c *gin.Context) {
	var req getAuthorityRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return
	}

	authority, err := s.store.GetAuthorityById(c, req.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Authority")
			return
		}
		internalServerError(c)
		return
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	if authority.UserID != authUser.ID {
		unauthorizedError(c)
		return
	}

	apiResponse(c, http.StatusOK, authority)
}

type deleteAuthorityRequest struct {
	ID int64 `uri:"id"`
}

func (s *Server) deleteAuthority(c *gin.Context) {
	var req deleteAuthorityRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	deletedAuthority, err := s.store.GetAuthorityById(c, req.ID)
	if err != nil {
		resourceNotFoundError(c, "Authority")
		return
	}

	if authUser.ID != deletedAuthority.UserID {
		unauthorizedError(c)
		return
	}

	params := db.DeleteAuthorityParams{
		ID:     req.ID,
		UserID: authUser.ID,
	}
	err = s.store.DeleteAuthority(c, params)
	if err != nil {
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusNoContent, nil)
}

func convertAuthorityResponse(auth db.Authority) authorityResponse {
	var lastNamePtr, emailPtr *string

	if auth.LastName.Valid {
		lastNamePtr = &auth.LastName.String
	}
	if auth.Email.Valid {
		emailPtr = &auth.Email.String
	}

	return authorityResponse{
		ID:              auth.ID,
		UserID:          auth.UserID,
		FirstName:       auth.FirstName,
		LastName:        lastNamePtr,
		PhoneNumber:     auth.PhoneNumber,
		Email:           emailPtr,
		IsDiscipleMaker: auth.IsDiscipleMaker,
		CreatedAt:       auth.CreatedAt,
	}
}
