package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

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
		if err != nil || !slices.Contains(roles, role) {
			errResp := NewErrorResponse(http.StatusForbidden, "FORBIDDEN", fmt.Errorf("this action needs the role %s", strings.Join(roles, " or ")))
			context.AbortWithStatusJSON(http.StatusForbidden, errResp)
			return
		}
		context.Next()
	}
}
