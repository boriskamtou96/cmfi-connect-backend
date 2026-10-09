package api

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/usernames"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

type BaseUserResponse struct {
	User        userResponse `json:"user"`
	AccessToken *string      `json:"accessToken,omitempty"`
}
type registerUserRequest struct {
	FirstName    string `json:"first_name" binding:"required"`
	LastName     string `json:"last_name" binding:"required"`
	HashPassword string `json:"hash_password" binding:"required,min=6"`
	PhoneNumber  string `json:"phone_number" binding:"required"`
	Email        string `json:"email"`
	// Chosen in the form; without it (older apps), one is made from the name.
	Username string `json:"username"`
}

type userResponse struct {
	ID          int64     `json:"id"`
	Username    string    `json:"username"`
	Role        string    `json:"role"`
	FirstName   string    `json:"first_name"`
	LastName    string    `json:"last_name"`
	PhoneNumber string    `json:"phone_number"`
	Email       string    `json:"email"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Server) registerUser(c *gin.Context) {
	var payload registerUserRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequestError(c, err)
		return
	}

	username := usernames.Normalize(payload.Username)
	if username != "" {
		if problem := usernames.Check(username); problem != "" {
			usernameError(c, problem)
			return
		}
	} else {
		suggestions, err := s.freeSuggestions(c, payload.FirstName, payload.LastName)
		if err != nil || len(suggestions) == 0 {
			internalServerError(c)
			return
		}
		username = suggestions[0]
	}

	hashPassword, err := bcrypt.GenerateFromPassword([]byte(payload.HashPassword), bcrypt.DefaultCost)
	if err != nil {
		internalServerError(c)
		return
	}

	arg := db.RegisterUserParams{
		Username:  username,
		FirstName: payload.FirstName,
		LastName: sql.NullString{
			String: payload.LastName,
			Valid:  payload.LastName != "",
		},
		Email: sql.NullString{
			String: payload.Email,
			Valid:  payload.Email != "",
		},
		PhoneNumber:  payload.PhoneNumber,
		HashPassword: hashPassword,
	}

	user, err := s.store.RegisterUser(c, arg)
	if err != nil {
		if isUniqueViolation(err, "users_username_key") {
			usernameError(c, usernames.Taken)
			return
		}
		if pgErr, ok := errors.AsType[*pq.Error](err); ok {
			switch pgErr.Code.Name() {
			case "unique_violation":
				forbiddenError(c)
				return
			}
		}
		internalServerError(c)
		return
	}

	_, err = s.store.CreateProfile(c, db.CreateProfileParams{
		UserID:    user.ID,
		FirstName: payload.FirstName,
		LastName: sql.NullString{
			String: payload.LastName,
			Valid:  payload.LastName != "",
		},
		Email: sql.NullString{
			String: payload.Email,
			Valid:  payload.Email != "",
		},
		PhoneNumber: payload.PhoneNumber,
	})
	if err != nil {
		if pgErr, ok := errors.AsType[*pq.Error](err); ok {
			switch pgErr.Code.Name() {
			case "unique_violation":
				forbiddenError(c)
				return
			}
		}
		internalServerError(c)
		return
	}

	apiResponse(c, http.StatusCreated, convertToUserResponse(user))
}

type loginRequest struct {
	Phone    string `json:"phone_number"`
	Password string `json:"password"`
}

func (s *Server) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}

	user, err := s.store.GetByPhoneNumber(c, req.Phone)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "User")
			return
		}
		internalServerError(c)
		return
	}

	err = utils.CheckPassword(req.Password, string(user.HashPassword))
	if err != nil {
		invalidCredentialsError(c)
		return
	}

	duration, err := time.ParseDuration(s.config.Token.Duration)
	if err != nil {
		internalServerError(c)
		return
	}
	accessToken, err := s.jwtAuthenticator.GenerateToken(user.ID, user.PhoneNumber, duration)
	if err != nil {
		internalServerError(c)
		return
	}

	apiResponse(c, http.StatusOK, convertToUserResponse(user, accessToken))
}

// GET /v1/users/me: the signed-in account, with its pseudo and role.
func (s *Server) getMe(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	user, err := s.store.GetUserById(c, authUser.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "user")
			return
		}
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusOK, convertToUserResponse(user))
}

type getUserRequest struct {
	ID int64 `uri:"id" binding:"required"`
}

// mayActOn tells whether the signed-in member may read or change account [id]:
// their own, or any for a member with one of [roles].
func (s *Server) mayActOn(c *gin.Context, id int64, roles ...string) bool {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if authUser.ID == id {
		return true
	}
	if len(roles) == 0 {
		return false
	}
	role, err := s.store.GetUserRole(c, authUser.ID)
	return err == nil && slices.Contains(roles, role)
}

// GET /v1/users/:id: one's own account; admins may read any.
func (s *Server) getUser(c *gin.Context) {
	var req getUserRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if !s.mayActOn(c, req.ID, roleAdmin, roleSuperadmin) {
		forbiddenError(c)
		return
	}

	user, err := s.store.GetUserById(c, req.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "user")
			return
		}
		internalServerError(c)
		return
	}

	apiResponse(c, http.StatusOK, convertToUserResponse(user))
}

type updateUserRequest struct {
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	HashPassword string `json:"hash_password"`
}

// PUT /v1/users/:id: one's own account only.
func (s *Server) updateUser(c *gin.Context) {
	// Get ID param and validate
	var req getUserRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if !s.mayActOn(c, req.ID) {
		forbiddenError(c)
		return
	}

	// Check update payload
	var payload updateUserRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequestError(c, err)
		return
	}

	// check if user exists
	_, err := s.store.GetUserById(c, req.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "user")
			return
		}
		internalServerError(c)
		return
	}

	// update user
	arg := db.UpdateUserParams{
		ID:        req.ID,
		FirstName: payload.FirstName,
		LastName: sql.NullString{
			String: payload.LastName,
			Valid:  payload.LastName != "",
		},
	}

	// hash the new password; nil keeps the current one (COALESCE in the query)
	if payload.HashPassword != "" {
		hashPassword, err := bcrypt.GenerateFromPassword([]byte(payload.HashPassword), bcrypt.DefaultCost)
		if err != nil {
			internalServerError(c)
			return
		}
		arg.HashPassword = hashPassword
	}

	updatedUser, err := s.store.UpdateUser(c, arg)
	if err != nil {
		internalServerError(c)
		return
	}

	// return updated user
	apiResponse(c, http.StatusOK, convertToUserResponse(updatedUser))
}

// DELETE /v1/users/:id: one's own account, or any for a superadmin.
func (s *Server) deleteUser(c *gin.Context) {
	var req getUserRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return
	}
	id := req.ID
	if !s.mayActOn(c, id, roleSuperadmin) {
		forbiddenError(c)
		return
	}

	_, err := s.store.GetUserById(c, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "user")
			return
		}
		badRequestError(c, err)
		return
	}

	if err := s.store.DeleteUser(c, id); err != nil {
		internalServerError(c)
		return
	}

	apiResponse(c, http.StatusNoContent, nil)
}

func convertToUserResponse(user db.User, accessToken ...string) BaseUserResponse {
	var tokenPtr *string

	// Si un token a été passé en paramètre, on l'ajoute
	if len(accessToken) > 0 && accessToken[0] != "" {
		tokenPtr = &accessToken[0]
	}
	return BaseUserResponse{
		User: userResponse{
			ID:          user.ID,
			Username:    user.Username,
			Role:        user.Role,
			FirstName:   user.FirstName,
			LastName:    user.LastName.String,
			Email:       user.Email.String,
			PhoneNumber: user.PhoneNumber,
			CreatedAt:   user.CreatedAt,
		},
		AccessToken: tokenPtr,
	}
}
