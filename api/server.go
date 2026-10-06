package api

import (
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Server struct {
	db     *gorm.DB
	config *utils.Config
}

func New(db *gorm.DB, cfg *utils.Config) *Server {
	return &Server{
		db:     db,
		config: cfg,
	}
}

func (s *Server) SetupRouter() *gin.Engine {
	router := gin.New()

	router.GET("/health", s.health)

	return router
}
