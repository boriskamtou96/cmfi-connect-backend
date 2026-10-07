package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
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
}

type userResponse struct {
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

	hashPassword, err := bcrypt.GenerateFromPassword([]byte(payload.HashPassword), bcrypt.DefaultCost)
	if err != nil {
		internalServerError(c)
		return
	}

	arg := db.RegisterUserParams{
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
		HashPassword: string(hashPassword),
	}

	user, err := s.store.RegisterUser(c, arg)
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

	err = utils.CheckPassword(req.Password, user.HashPassword)
	if err != nil {
		invalidCredentialsError(c)
		return
	}

	duration, err := time.ParseDuration(s.config.Token.Duration)
	if err != nil {
		internalServerError(c)
		return
	}
	accessToken, err := s.tokenMaker.GenerateToken(req.Phone, duration)
	if err != nil {
		internalServerError(c)
		return
	}

	apiResponse(c, http.StatusOK, convertToUserResponse(user, accessToken))
}

type getUserRequest struct {
	ID int64 `uri:"id" binding:"required,min=1"`
}

func (s *Server) getUser(c *gin.Context) {
	// Get ID param and validate
	var req getUserRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
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

func (s *Server) updateUser(c *gin.Context) {
	// Get ID param and validate
	var req getUserRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
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
		HashPassword: payload.HashPassword,
	}

	updatedUser, err := s.store.UpdateUser(c, arg)
	if err != nil {
		internalServerError(c)
		return
	}

	// return updated user
	apiResponse(c, http.StatusOK, convertToUserResponse(updatedUser))
}

func (s *Server) deleteUser(c *gin.Context) {
	// Get ID param
	idParam := c.Param("userID")
	id, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		badRequestError(c, err)
		return
	}

	_, err = s.store.GetUserById(c, id)
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
			FirstName:   user.FirstName,
			LastName:    user.LastName.String,
			Email:       user.Email.String,
			PhoneNumber: user.PhoneNumber,
			CreatedAt:   user.CreatedAt,
		},
		AccessToken: tokenPtr,
	}
}
