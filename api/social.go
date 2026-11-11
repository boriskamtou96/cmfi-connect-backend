package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/socialauth"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/usernames"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// Sign-in with Google or Apple: the app signs in with Firebase
// Authentication and sends its ID token (firebase_token). A Google or Apple
// account signs in to the CMFI account it is linked to; the first time, to
// the account that gave the same (verified) email, else the member completes
// a profile (POST /v1/users/social/register) or links an account they
// already have with its number and password (POST /v1/users/social/link).

type socialSignInRequest struct {
	FirebaseToken string `json:"firebase_token" binding:"required"`
}

// identityOf checks the token; false once the error is answered: 503
// SOCIAL_SIGN_IN_UNAVAILABLE without Firebase on the server, 401
// INVALID_SOCIAL_TOKEN for a token Firebase didn't give (or expired).
func (s *Server) identityOf(c *gin.Context, firebaseToken string) (socialauth.Identity, bool) {
	identity, err := s.identities.Identity(c, firebaseToken)
	switch {
	case errors.Is(err, socialauth.ErrUnavailable):
		c.JSON(http.StatusServiceUnavailable, NewErrorResponse(http.StatusServiceUnavailable,
			"SOCIAL_SIGN_IN_UNAVAILABLE", err))
		return identity, false
	case err != nil:
		c.JSON(http.StatusUnauthorized, NewErrorResponse(http.StatusUnauthorized,
			"INVALID_SOCIAL_TOKEN", err))
		return identity, false
	}
	return identity, true
}

// accountOf is the account [identity] signs in to: the one linked to it, else
// the one that gave its verified email (then linked). found false without
// one; err for a failure of the database.
func (s *Server) accountOf(c *gin.Context, identity socialauth.Identity) (user db.User, found bool, err error) {
	user, err = s.store.GetUserByIdentity(c, db.GetUserByIdentityParams{
		Provider: identity.Provider,
		Subject:  identity.Subject,
	})
	if err == nil {
		return user, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return user, false, err
	}
	if !identity.EmailVerified || identity.Email == "" {
		return user, false, nil
	}
	user, err = s.store.GetUserByEmail(c, identity.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return user, false, nil
	}
	if err != nil {
		return user, false, err
	}
	if err := s.link(c, identity, user.ID); err != nil {
		return user, false, err
	}
	logFor(c).Info("google or apple account linked by its email", "user_id", user.ID, "provider", identity.Provider)
	return user, true, nil
}

func (s *Server) link(c *gin.Context, identity socialauth.Identity, userID int64) error {
	return s.store.CreateUserIdentity(c, db.CreateUserIdentityParams{
		Provider: identity.Provider,
		Subject:  identity.Subject,
		UserID:   userID,
		Email:    identity.Email,
	})
}

// POST /v1/users/social: signed in with the account a Google or Apple account
// is linked to (200, like the login). 404 NO_ACCOUNT the first time without
// an account of the same email: the app asks for the rest of the profile.
func (s *Server) socialSignIn(c *gin.Context) {
	var req socialSignInRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	identity, ok := s.identityOf(c, req.FirebaseToken)
	if !ok {
		return
	}
	user, found, err := s.accountOf(c, identity)
	if err != nil {
		internalServerError(c, err)
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, NewErrorResponse(http.StatusNotFound, "NO_ACCOUNT",
			fmt.Errorf("no account for this %s account yet", identity.Provider)))
		return
	}
	s.signedIn(c, user, http.StatusOK)
}

type socialRegisterRequest struct {
	FirebaseToken string `json:"firebase_token" binding:"required"`
	FirstName     string `json:"first_name" binding:"required,max=255"`
	LastName      string `json:"last_name" binding:"max=255"`
	PhoneNumber   string `json:"phone_number" binding:"required,max=32"`
	// without it, one is made from the name
	Username string `json:"username"`
}

// POST /v1/users/social/register: the account of a Google or Apple account,
// with the profile the member completed (201, signed in). Its email is the
// one of the Google or Apple account. An account already there for it signs
// in (200). 409 PHONE_TAKEN when the number has an account: the member
// links it with its password instead (POST /v1/users/social/link).
func (s *Server) socialRegister(c *gin.Context) {
	var req socialRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	req.PhoneNumber = strings.TrimSpace(req.PhoneNumber)
	if req.FirstName == "" || req.PhoneNumber == "" {
		badRequestError(c, errors.New("first_name and phone_number are required"))
		return
	}
	identity, ok := s.identityOf(c, req.FirebaseToken)
	if !ok {
		return
	}
	// sent twice (a network error on the answer), or an account of its email
	if user, found, err := s.accountOf(c, identity); err != nil {
		internalServerError(c, err)
		return
	} else if found {
		s.signedIn(c, user, http.StatusOK)
		return
	}

	if _, err := s.store.GetByPhoneNumber(c, req.PhoneNumber); err == nil {
		phoneTaken(c)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		internalServerError(c, err)
		return
	}

	username := usernames.Normalize(req.Username)
	if username != "" {
		if problem := usernames.Check(username); problem != "" {
			usernameError(c, problem)
			return
		}
	} else {
		suggestions, err := s.freeSuggestions(c, req.FirstName, req.LastName)
		if err != nil || len(suggestions) == 0 {
			internalServerError(c, err)
			return
		}
		username = suggestions[0]
	}

	hash, err := unknownPassword()
	if err != nil {
		internalServerError(c, err)
		return
	}
	email := ""
	if identity.EmailVerified {
		email = identity.Email
	}
	user, err := s.store.RegisterSocialUserTx(c, db.RegisterSocialUserTxParams{
		User: db.RegisterSocialUserParams{
			FirstName:    req.FirstName,
			LastName:     sql.NullString{String: req.LastName, Valid: req.LastName != ""},
			PhoneNumber:  req.PhoneNumber,
			Email:        sql.NullString{String: email, Valid: email != ""},
			HashPassword: hash,
			Username:     username,
		},
		Provider:      identity.Provider,
		Subject:       identity.Subject,
		IdentityEmail: identity.Email,
	})
	switch {
	case isUniqueViolation(err, "users_username_key"):
		usernameError(c, usernames.Taken)
		return
	case isUniqueViolation(err, "users_phone_number_key"):
		phoneTaken(c)
		return
	case err != nil:
		internalServerError(c, err)
		return
	}
	s.signedIn(c, user, http.StatusCreated)
}

type socialLinkRequest struct {
	FirebaseToken string `json:"firebase_token" binding:"required"`
	PhoneNumber   string `json:"phone_number" binding:"required"`
	Password      string `json:"password" binding:"required"`
}

// POST /v1/users/social/link: a member who already has an account (its
// number and password) signs in with Google or Apple from now on (200,
// signed in). 401 INVALID_CREDENTIALS for a wrong number or password, 409
// IDENTITY_TAKEN when the Google or Apple account signs in to another one.
func (s *Server) socialLink(c *gin.Context) {
	var req socialLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	identity, ok := s.identityOf(c, req.FirebaseToken)
	if !ok {
		return
	}
	user, err := s.store.GetByPhoneNumber(c, strings.TrimSpace(req.PhoneNumber))
	if errors.Is(err, sql.ErrNoRows) {
		invalidCredentialsError(c)
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	if !user.HasPassword || utils.CheckPassword(req.Password, string(user.HashPassword)) != nil {
		invalidCredentialsError(c)
		return
	}

	linked, err := s.store.GetUserByIdentity(c, db.GetUserByIdentityParams{
		Provider: identity.Provider,
		Subject:  identity.Subject,
	})
	switch {
	case err == nil && linked.ID != user.ID:
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "IDENTITY_TAKEN",
			fmt.Errorf("this %s account signs in to another account", identity.Provider)))
		return
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		internalServerError(c, err)
		return
	}
	if err := s.link(c, identity, user.ID); err != nil {
		internalServerError(c, err)
		return
	}
	s.signedIn(c, user, http.StatusOK)
}

func phoneTaken(c *gin.Context) {
	c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "PHONE_TAKEN",
		errors.New("this phone number already has an account")))
}

// unknownPassword is the hash of a random password nobody knows: the
// account of a Google or Apple account signs in only with it until the
// member chooses one.
func unknownPassword() ([]byte, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(secret)), bcrypt.DefaultCost)
}
