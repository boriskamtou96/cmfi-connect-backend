package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

type errorResponse struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewErrorResponse(status int, code string, err error) errorResponse {
	return errorResponse{
		Status:  status,
		Code:    code,
		Message: err.Error(),
	}
}

type ApiResponse struct {
	Status  int `json:"status"`
	Content any `json:"content"`
}

func NewApiResponse(status int, content any) ApiResponse {
	return ApiResponse{
		Status:  status,
		Content: content,
	}
}

func apiResponse(c *gin.Context, status int, data any) {
	apiResponse := ApiResponse{status, data}
	c.JSON(status, apiResponse)
}

func badRequestError(c *gin.Context) {
	errorResponse := NewErrorResponse(http.StatusBadRequest, "BAD_REQUEST", fmt.Errorf("bad request"))
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

func resourceNotFoundError(c *gin.Context, resourceName string) {
	errorResponse := NewErrorResponse(http.StatusNotFound, "NOT_FOUND", fmt.Errorf("%s not found", resourceName))
	c.JSON(http.StatusNotFound, errorResponse)
}

func methodNotAllowError(c *gin.Context) {
	errorResponse := NewErrorResponse(http.StatusMethodNotAllowed, "METHOD_NOT_ALLOW", fmt.Errorf("%s not allow", c.Request.Method))
	c.JSON(http.StatusNotFound, errorResponse)
}
