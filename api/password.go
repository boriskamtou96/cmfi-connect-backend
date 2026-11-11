package api

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/phoneauth"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
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

type changeMyPasswordRequest struct {
	// the password now: not needed by an account made with Google or Apple,
	// which has none yet
	CurrentPassword string `json:"current_password"`
	Password        string `json:"password" binding:"required,min=6,max=72"`
}

// PUT /v1/users/me/password: the member chooses their password (settings,
// or the one an admin gave, changed at the next sign-in). 200 with the
// account (must_change_password false). 401 INVALID_CREDENTIALS for a wrong
// current password.
func (s *Server) changeMyPassword(c *gin.Context) {
	var req changeMyPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	user, err := s.store.GetUserById(c, authUser.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "user")
			return
		}
		internalServerError(c, err)
		return
	}
	if user.HasPassword && utils.CheckPassword(req.CurrentPassword, string(user.HashPassword)) != nil {
		invalidCredentialsError(c)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		internalServerError(c, err)
		return
	}
	if err := s.store.UpdateUserPassword(c, db.UpdateUserPasswordParams{ID: user.ID, HashPassword: hash}); err != nil {
		internalServerError(c, err)
		return
	}
	user.HashPassword, user.MustChangePassword, user.HasPassword = hash, false, true
	apiResponse(c, http.StatusOK, convertToUserResponse(user))
}

type temporaryPasswordResponse struct {
	TemporaryPassword string `json:"temporary_password"`
}

// POST /v1/admin/members/:id/password: a member forgot their password, an
// admin gives them a temporary one (shown once, to pass on by hand); the
// member chooses theirs at the next sign-in. Free, unlike the SMS. Only a
// superadmin resets the password of an admin or a superadmin, and nobody
// their own (403 FORBIDDEN).
func (s *Server) adminResetPassword(c *gin.Context) {
	var uri getUserRequest
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if uri.ID == authUser.ID {
		forbiddenError(c)
		return
	}
	member, err := s.store.GetUserById(c, uri.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "user")
			return
		}
		internalServerError(c, err)
		return
	}
	if member.Role == roleAdmin || member.Role == roleSuperadmin {
		role, err := s.store.GetUserRole(c, authUser.ID)
		if err != nil || role != roleSuperadmin {
			forbiddenError(c)
			return
		}
	}

	password, err := temporaryPassword()
	if err != nil {
		internalServerError(c, err)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		internalServerError(c, err)
		return
	}
	if err := s.store.SetTemporaryPassword(c, db.SetTemporaryPasswordParams{ID: member.ID, HashPassword: hash}); err != nil {
		internalServerError(c, err)
		return
	}
	logFor(c).Info("temporary password given", "member_id", member.ID, "by", authUser.ID)
	apiResponse(c, http.StatusOK, temporaryPasswordResponse{TemporaryPassword: password})
}

// temporaryAlphabet: letters and digits read aloud or typed without doubt
// (no 0/o, 1/l/i).
const temporaryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// temporaryPassword: "k7m2-p9qx", easy to pass on by phone or WhatsApp.
func temporaryPassword() (string, error) {
	var b strings.Builder
	limit := big.NewInt(int64(len(temporaryAlphabet)))
	for i := range 8 {
		if i == 4 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		b.WriteByte(temporaryAlphabet[n.Int64()])
	}
	return b.String(), nil
}
