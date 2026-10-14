package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/usernames"
	"github.com/gin-gonic/gin"
)

// The life of an authority found by pseudo: the member asked is the one who
// accepts. An authority typed by hand is accepted at once.
const (
	authorityPending  = "pending"
	authorityAccepted = "accepted"
	authorityDeclined = "declined"
)

type authorityResponse struct {
	ID              int64     `json:"id"`
	UserID          int64     `json:"user_id"`
	FirstName       string    `json:"first_name"`
	LastName        *string   `json:"last_name"`
	PhoneNumber     *string   `json:"phone_number"`
	Email           *string   `json:"email"`
	IsDiscipleMaker bool      `json:"is_disciple_maker"`
	CreatedAt       time.Time `json:"created_at"`

	// A member of the app: their pseudo, church and assembly. Their phone and
	// e-mail come once they accepted.
	Username *string `json:"username"`
	Church   *string `json:"church"`
	Assembly *string `json:"assembly"`
	Status   string  `json:"status"`
	// their profile photo, or null
	Avatar *mediaResponse `json:"avatar"`
	// whether the owner shows their regularity to them (a disciple maker)
	SharesRegularity bool `json:"shares_regularity"`
}

func nullable(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nonEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// authorityResponses gives the authorities as their owner sees them: a linked
// member shows their current name, and their contact once they accepted.
func (s *Server) authorityResponses(c *gin.Context, authorities []db.Authority) ([]authorityResponse, error) {
	ids := make([]int64, 0, len(authorities))
	for _, authority := range authorities {
		if authority.LinkedUserID.Valid {
			ids = append(ids, authority.LinkedUserID.Int64)
		}
	}
	cards := map[int64]db.ListMemberCardsRow{}
	if len(ids) > 0 {
		rows, err := s.store.ListMemberCards(c, ids)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			cards[row.ID] = row
		}
	}

	responses := make([]authorityResponse, 0, len(authorities))
	for _, authority := range authorities {
		response := authorityResponse{
			ID:               authority.ID,
			UserID:           authority.UserID,
			FirstName:        authority.FirstName,
			LastName:         nullable(authority.LastName),
			PhoneNumber:      nullable(authority.PhoneNumber),
			Email:            nullable(authority.Email),
			IsDiscipleMaker:  authority.IsDiscipleMaker,
			CreatedAt:        authority.CreatedAt,
			Status:           authority.Status,
			SharesRegularity: authority.SharesRegularity,
		}
		if card, ok := cards[authority.LinkedUserID.Int64]; ok && authority.LinkedUserID.Valid {
			response.FirstName = card.FirstName
			response.LastName = nonEmpty(card.LastName)
			response.Username = &card.Username
			response.Church = nonEmpty(card.Church)
			response.Assembly = nonEmpty(card.Assembly)
			response.Avatar = toMemberCard(card).Avatar
			response.PhoneNumber, response.Email = nil, nil
			if authority.Status == authorityAccepted {
				response.PhoneNumber = nonEmpty(card.PhoneNumber)
				response.Email = nonEmpty(card.Email)
			}
		}
		responses = append(responses, response)
	}
	return responses, nil
}

type createAuthorityPayload struct {
	// A member of the app, found by pseudo; or else the fields below, typed by hand.
	Username        string `json:"username"`
	FirstName       string `json:"first_name"`
	LastName        string `json:"last_name"`
	PhoneNumber     string `json:"phone_number"`
	Email           string `json:"email"`
	IsDiscipleMaker bool   `json:"is_disciple_maker"`
}

// POST /v1/authorities
func (s *Server) createAuthority(c *gin.Context) {
	var payload createAuthorityPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	params := db.CreateUserAuthorityParams{
		IsDiscipleMaker: payload.IsDiscipleMaker,
		UserID:          authUser.ID,
		Status:          authorityAccepted,
	}
	if username := usernames.Normalize(payload.Username); username != "" {
		card, err := s.memberCardByUsername(c, username)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				resourceNotFoundError(c, "member")
				return
			}
			internalServerError(c)
			return
		}
		if card.ID == authUser.ID {
			badRequestError(c, fmt.Errorf("you can't be your own authority"))
			return
		}
		// the name as it was when asked; the live one is shown from the account
		params.FirstName = card.FirstName
		params.LastName = sql.NullString{String: card.LastName, Valid: card.LastName != ""}
		params.LinkedUserID = sql.NullInt64{Int64: card.ID, Valid: true}
		params.Status = authorityPending
	} else {
		if payload.FirstName == "" || payload.PhoneNumber == "" {
			badRequestError(c, fmt.Errorf("first_name and phone_number are required without username"))
			return
		}
		params.FirstName = payload.FirstName
		params.LastName = sql.NullString{String: payload.LastName, Valid: payload.LastName != ""}
		params.PhoneNumber = sql.NullString{String: payload.PhoneNumber, Valid: true}
		params.Email = sql.NullString{String: payload.Email, Valid: payload.Email != ""}
	}

	authority, err := s.store.CreateUserAuthority(c, params)
	if err != nil {
		switch {
		case isUniqueViolation(err, "idx_authorities_user_linked"):
			c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "AUTHORITY_EXISTS",
				fmt.Errorf("this member is already one of your authorities")))
		case isUniqueViolation(err, "idx_unique_user_disciple_maker"):
			c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "DISCIPLE_MAKER_EXISTS",
				fmt.Errorf("you already have a disciple maker")))
		default:
			internalServerError(c)
		}
		return
	}

	responses, err := s.authorityResponses(c, []db.Authority{authority})
	if err != nil {
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusCreated, responses[0])
}

type getUserAuthoritiesParams struct {
	Page int32 `form:"page" binding:"required,min=1"`
	Size int32 `form:"size" binding:"required,min=5,max=10"`
}

// GET /v1/authorities?page=&size=: the signed-in member's authorities.
func (s *Server) listAuthorities(c *gin.Context) {
	var req getUserAuthoritiesParams
	if err := c.ShouldBind(&req); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	params := db.GetUserAuthoritiesParams{
		UserID: authUser.ID,
		Limit:  req.Size,
		Offset: (req.Page - 1) * req.Size,
	}
	authorities, err := s.store.GetUserAuthorities(c, params)
	if err != nil {
		internalServerError(c)
		return
	}
	responses, err := s.authorityResponses(c, authorities)
	if err != nil {
		internalServerError(c)
		return
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
	paginatedApiResponse(c, http.StatusOK, responses, meta)
}

type authorityIDRequest struct {
	ID int64 `uri:"id" binding:"required"`
}

// ownAuthority loads an authority of the signed-in member; anyone else's is
// answered 404, like one that doesn't exist.
func (s *Server) ownAuthority(c *gin.Context) (db.Authority, bool) {
	var req authorityIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return db.Authority{}, false
	}
	authority, err := s.store.GetAuthorityById(c, req.ID)
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if err != nil || authority.UserID != authUser.ID {
		if err == nil || errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Authority")
		} else {
			internalServerError(c)
		}
		return db.Authority{}, false
	}
	return authority, true
}

// GET /v1/authorities/:id
func (s *Server) getAuthorityById(c *gin.Context) {
	authority, ok := s.ownAuthority(c)
	if !ok {
		return
	}
	responses, err := s.authorityResponses(c, []db.Authority{authority})
	if err != nil {
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusOK, responses[0])
}

// DELETE /v1/authorities/:id
func (s *Server) deleteAuthority(c *gin.Context) {
	authority, ok := s.ownAuthority(c)
	if !ok {
		return
	}
	err := s.store.DeleteAuthority(c, db.DeleteAuthorityParams{ID: authority.ID, UserID: authority.UserID})
	if err != nil {
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusNoContent, nil)
}

type authoritySharingRequest struct {
	Shares *bool `json:"shares" binding:"required"`
}

// PUT /v1/authorities/:id/sharing: whether the signed-in member shows their
// regularity (the days they filled in) to this authority, their disciple maker.
func (s *Server) setAuthoritySharing(c *gin.Context) {
	authority, ok := s.ownAuthority(c)
	if !ok {
		return
	}
	var req authoritySharingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}

	updated, err := s.store.SetAuthoritySharing(c, db.SetAuthoritySharingParams{
		ID:               authority.ID,
		UserID:           authority.UserID,
		SharesRegularity: *req.Shares,
	})
	if err != nil {
		// deleted in the meantime
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Authority")
			return
		}
		internalServerError(c)
		return
	}
	responses, err := s.authorityResponses(c, []db.Authority{updated})
	if err != nil {
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusOK, responses[0])
}

// authorityRequestResponse is a member asking the signed-in member to accompany them.
type authorityRequestResponse struct {
	ID              int64      `json:"id"`
	From            memberCard `json:"from"`
	IsDiscipleMaker bool       `json:"is_disciple_maker"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
}

func (s *Server) authorityRequestResponses(c *gin.Context, requests []db.Authority) ([]authorityRequestResponse, error) {
	ids := make([]int64, len(requests))
	for i, request := range requests {
		ids[i] = request.UserID
	}
	cards := map[int64]db.ListMemberCardsRow{}
	if len(ids) > 0 {
		rows, err := s.store.ListMemberCards(c, ids)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			cards[row.ID] = row
		}
	}
	responses := make([]authorityRequestResponse, 0, len(requests))
	for _, request := range requests {
		responses = append(responses, authorityRequestResponse{
			ID:              request.ID,
			From:            toMemberCard(cards[request.UserID]),
			IsDiscipleMaker: request.IsDiscipleMaker,
			Status:          request.Status,
			CreatedAt:       request.CreatedAt,
		})
	}
	return responses, nil
}

// GET /v1/authorities/requests: who asks the signed-in member to accompany them.
func (s *Server) listAuthorityRequests(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	requests, err := s.store.ListAuthorityRequests(c, sql.NullInt64{Int64: authUser.ID, Valid: true})
	if err != nil {
		internalServerError(c)
		return
	}
	responses, err := s.authorityRequestResponses(c, requests)
	if err != nil {
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusOK, responses)
}

// POST /v1/authorities/requests/:id/accept and .../decline
func (s *Server) answerAuthorityRequest(status string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req authorityIDRequest
		if err := c.ShouldBindUri(&req); err != nil {
			badRequestError(c, err)
			return
		}
		authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

		answered, err := s.store.AnswerAuthorityRequest(c, db.AnswerAuthorityRequestParams{
			ID:           req.ID,
			LinkedUserID: sql.NullInt64{Int64: authUser.ID, Valid: true},
			Status:       status,
		})
		if err != nil {
			// not asked to this member, or already answered
			if errors.Is(err, sql.ErrNoRows) {
				resourceNotFoundError(c, "Request")
				return
			}
			internalServerError(c)
			return
		}
		responses, err := s.authorityRequestResponses(c, []db.Authority{answered})
		if err != nil {
			internalServerError(c)
			return
		}
		apiResponse(c, http.StatusOK, responses[0])
	}
}
