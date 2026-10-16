package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

const (
	authorizationHeaderKey  = "authorization"
	authorizationType       = "Bearer"
	authorizationPayloadKey = "authorizationPayloadKey"
)

func authMiddleware(jwtAuthenticator token.JWTAuthenticator) gin.HandlerFunc {
	return func(context *gin.Context) {
		authHeader := context.GetHeader(authorizationHeaderKey)

		if authHeader == "" || len(authHeader) == 0 {
			errResp := NewErrorResponse(http.StatusUnauthorized, "UNAUTHORIZED", fmt.Errorf("authorization header is not provided"))
			context.AbortWithStatusJSON(http.StatusUnauthorized, errResp)
			return
		}

		field := strings.Fields(authHeader)
		if len(field) < 2 {
			errResp := NewErrorResponse(http.StatusUnauthorized, "UNAUTHORIZED", fmt.Errorf("invalid authorization header format"))
			context.AbortWithStatusJSON(http.StatusUnauthorized, errResp)
			return
		}

		authType := field[0]
		if authType != authorizationType {
			errResp := NewErrorResponse(http.StatusUnauthorized, "UNAUTHORIZED", fmt.Errorf("unsupported authorization type"))
			context.AbortWithStatusJSON(http.StatusUnauthorized, errResp)
			return
		}

		accessToken := field[1]
		payload, err := jwtAuthenticator.VerifyToken(accessToken)
		if err != nil {
			errResp := NewErrorResponse(http.StatusUnauthorized, "Error", err)
			context.AbortWithStatusJSON(http.StatusUnauthorized, errResp)
			return
		}

		context.Set(authorizationPayloadKey, payload)
		context.Next()
	}
}

// The roles of the members, from the least to the most rights.
const (
	roleUser       = "user"
	roleAdmin      = "admin"
	roleSuperadmin = "superadmin"
)

// requireRole lets through the members who have one of [roles]. The role is read
// from the database, not from the token: a member who loses a role loses its
// rights at once. Goes after authMiddleware.
func requireRole(store db.Querier, roles ...string) gin.HandlerFunc {
	return func(context *gin.Context) {
		authUser := context.MustGet(authorizationPayloadKey).(*token.Payload)
		role, err := store.GetUserRole(context, authUser.ID)
		if err != nil {
			logFor(context).Error("cannot read the role", "error", err)
		}
		if err != nil || !slices.Contains(roles, role) {
			errResp := NewErrorResponse(http.StatusForbidden, "FORBIDDEN", fmt.Errorf("this action needs the role %s", strings.Join(roles, " or ")))
			context.AbortWithStatusJSON(http.StatusForbidden, errResp)
			return
		}
		context.Next()
	}
}

// ----- logs -----

const (
	requestIDHeader = "X-Request-ID"
	requestIDKey    = "requestID"
)

// a request id given by a client is kept when it is short and plain
var plainID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// requestID names each request: the X-Request-ID of the client (the apps may
// send theirs) or a new one, sent back in the same header. Every log line of
// the request carries it, so an error seen in an app leads to its lines here.
func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestIDHeader)
		if !plainID.MatchString(id) {
			id = newRequestID()
		}
		c.Set(requestIDKey, id)
		c.Header(requestIDHeader, id)
		c.Next()
	}
}

func newRequestID() string {
	bytes := make([]byte, 8)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// logFor is the logger of a request: its id, method and route.
func logFor(c *gin.Context) *slog.Logger {
	return slog.Default().With(
		"request_id", c.GetString(requestIDKey),
		"method", c.Request.Method,
		"route", route(c),
	)
}

// route is the pattern of the route ("/v1/admin/members/:id"), never the
// path nor the query string: they may hold a name or a phone number.
func route(c *gin.Context) string {
	if pattern := c.FullPath(); pattern != "" {
		return pattern
	}
	return "unmatched"
}

// logRequests writes one line per request once answered: route, status,
// duration, size and the account (its number). 5xx are errors, 4xx
// warnings; the health check of the host only in debug.
func logRequests() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		attributes := []any{
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes", max(c.Writer.Size(), 0),
		}
		if payload, ok := c.Get(authorizationPayloadKey); ok {
			if user, ok := payload.(*token.Payload); ok {
				attributes = append(attributes, "user_id", user.ID)
			}
		}
		logger := logFor(c)
		switch {
		case status >= http.StatusInternalServerError:
			logger.Error("request", attributes...)
		case status >= http.StatusBadRequest:
			logger.Warn("request", attributes...)
		case c.FullPath() == "/v1/health":
			logger.Debug("request", attributes...)
		default:
			logger.Info("request", attributes...)
		}
	}
}

// recoverPanics answers 500 when a handler panics, and logs the panic with
// its stack.
func recoverPanics() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, recovered any) {
		logFor(c).Error("panic", "panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, NewErrorResponse(
			http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", fmt.Errorf("internal server error"),
		))
	})
}
