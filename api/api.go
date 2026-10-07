package api

import (
	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/gin-gonic/gin"
)

type Server struct {
	store  *db.SQLStore
	config *utils.Config
}

func New(store *db.SQLStore, cfg *utils.Config) *Server {
	return &Server{
		store:  store,
		config: cfg,
	}
}

func (s *Server) SetupRouter() *gin.Engine {
	router := gin.New()

	router.NoRoute(func(c *gin.Context) {
		resourceNotFoundError(c, "ROUTE")
	})

	router.NoMethod(func(c *gin.Context) {
		methodNotAllowError(c)
	})

	r := router.Group("v1")
	{
		r.GET("/health", s.health)

		users := r.Group("users")
		{
			users.POST("/register", s.registerUser)
			users.GET("/:userID", s.getUser)
			users.PUT("/:userID", s.updateUser)
		}
	}

	return router
}
