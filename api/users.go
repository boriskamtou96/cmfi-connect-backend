package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

type BaseUserResponse struct {
	User userResponse `json:"user"`
}
type registerUserRequest struct {
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	HashPassword string `json:"hash_password"`
}

type userResponse struct {
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) registerUser(c *gin.Context) {
	var payload registerUserRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequestError(c)
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
			Valid:  true,
		},
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

func (s *Server) getUser(c *gin.Context) {
	idParam := c.Param("userID")
	id, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		badRequestError(c)
		return
	}

	user, err := s.store.GetUserById(c, id)
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
	// Get ID param
	idParam := c.Param("userID")
	id, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		badRequestError(c)
		return
	}

	// Check update payload
	var payload updateUserRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequestError(c)
		return
	}

	// check if user exists
	_, err = s.store.GetUserById(c, id)
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
		ID:        id,
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

func convertToUserResponse(user db.User) BaseUserResponse {
	return BaseUserResponse{
		User: userResponse{
			FirstName: user.FirstName,
			LastName:  user.LastName.String,
			CreatedAt: user.CreatedAt,
		},
	}
}
