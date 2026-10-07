package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

const (
	authorizationHeaderKey  = "authorization"
	authorizationType       = "bearer"
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
