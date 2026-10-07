package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

type MetaResponse struct {
	Total  int64 `json:"total"`
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

type PaginatedResponse struct {
	Status  int          `json:"status"`
	Content any          `json:"content"`
	Meta    MetaResponse `json:"meta"`
}

func NewPaginatedResponse(status int, content any, meta MetaResponse) PaginatedResponse {
	return PaginatedResponse{
		Status:  status,
		Content: content,
		Meta: MetaResponse{
			Total:  meta.Total,
			Limit:  meta.Limit,
			Offset: meta.Offset,
		},
	}
}

type ErrorResponse struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewErrorResponse(status int, code string, err error) ErrorResponse {
	return ErrorResponse{
		Status:  status,
		Code:    code,
		Message: err.Error(),
	}
}

type Response struct {
	Status  int `json:"status"`
	Content any `json:"content"`
}

func NewApiResponse(status int, content any) Response {
	return Response{
		Status:  status,
		Content: content,
	}
}

func apiResponse(c *gin.Context, status int, data any) {
	apiResponse := NewApiResponse(status, data)
	c.JSON(status, apiResponse)
}

func paginatedApiResponse(c *gin.Context, status int, content any, meta MetaResponse) {
	apiResponse := NewPaginatedResponse(status, content, meta)
	c.JSON(status, apiResponse)
}

func badRequestError(c *gin.Context, err error) {
	errorResponse := NewErrorResponse(http.StatusBadRequest, "BAD_REQUEST", fmt.Errorf("bad request: %s", err))
	c.JSON(http.StatusForbidden, errorResponse)
}

func forbiddenError(c *gin.Context) {
	errorResponse := NewErrorResponse(http.StatusForbidden, "FORBIDDEN", fmt.Errorf("forbidden"))
	c.JSON(http.StatusForbidden, errorResponse)
}

func internalServerError(c *gin.Context) {
	errorResponse := NewErrorResponse(http.StatusForbidden, "INTERNAL_SERVER_ERROR", fmt.Errorf("internal server error"))
	c.JSON(http.StatusInternalServerError, errorResponse)
}

func unauthorizedError(c *gin.Context) {
	errorResponse := NewErrorResponse(http.StatusUnauthorized, "UNAUTHORIZED", fmt.Errorf("unauthorized action"))
	c.JSON(http.StatusUnauthorized, errorResponse)
}

func invalidCredentialsError(c *gin.Context) {
	errorResponse := NewErrorResponse(http.StatusUnauthorized, "INVALID_CREDENTIALS", fmt.Errorf("invalid credentials"))
	c.JSON(http.StatusUnauthorized, errorResponse)
}

func resourceNotFoundError(c *gin.Context, resourceName string) {
	errorResponse := NewErrorResponse(http.StatusNotFound, "NOT_FOUND", fmt.Errorf("%s not found", resourceName))
	c.JSON(http.StatusNotFound, errorResponse)
}

func methodNotAllowError(c *gin.Context) {
	errorResponse := NewErrorResponse(http.StatusMethodNotAllowed, "METHOD_NOT_ALLOW", fmt.Errorf("%s not allow", c.Request.Method))
	c.JSON(http.StatusNotFound, errorResponse)
}
