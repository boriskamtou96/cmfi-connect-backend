package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/phoneauth"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type resetPasswordRequest struct {
	// The ID token Firebase gave the app after the SMS code.
	FirebaseToken string `json:"firebase_token" binding:"required"`
	Password      string `json:"password" binding:"required,min=6"`
}

// POST /v1/users/password/reset: a member who forgot their password proves
// they hold their phone (an SMS code, checked by Firebase in the app) and
// chooses a new one, then signs in with it. 401 INVALID_PHONE_VERIFICATION
// for a token Firebase didn't give (or older than 10 minutes), 404 when no
// account uses the number, 503 when the server has no Firebase key.
func (s *Server) resetPassword(c *gin.Context) {
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}

	phone, err := s.phones.VerifiedPhone(c, req.FirebaseToken)
	switch {
	case errors.Is(err, phoneauth.ErrUnavailable):
		c.JSON(http.StatusServiceUnavailable, NewErrorResponse(http.StatusServiceUnavailable,
			"PASSWORD_RESET_UNAVAILABLE", fmt.Errorf("the password can't be reset on this server")))
		return
	case err != nil:
		c.JSON(http.StatusUnauthorized, NewErrorResponse(http.StatusUnauthorized,
			"INVALID_PHONE_VERIFICATION", err))
		return
	}

	user, err := s.store.GetByPhoneNumber(c, phone)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "User")
			return
		}
		internalServerError(c, err)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		internalServerError(c, err)
		return
	}
	if err := s.store.UpdateUserPassword(c, db.UpdateUserPasswordParams{
		ID:           user.ID,
		HashPassword: hash,
	}); err != nil {
		internalServerError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
