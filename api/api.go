package api

import (
	"fmt"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/gin-gonic/gin"
)

type Server struct {
	store            *db.SQLStore
	config           *utils.Config
	jwtAuthenticator token.JWTAuthenticator
}

func New(store *db.SQLStore, cfg *utils.Config) (*Server, error) {
	jwtAuthenticator, err := token.NewPasetoMaker(cfg.Token.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("token.NewPasetoMaker: %w", err)
	}

	return &Server{
		store:            store,
		config:           cfg,
		jwtAuthenticator: jwtAuthenticator,
	}, nil
}

func (s *Server) SetupRouter() *gin.Engine {
	router := gin.Default()

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
			users.POST("/login", s.login)
			usersWithID := users.Group("/:id")
			{
				usersWithID.GET("/", s.getUser)
				usersWithID.PUT("/", s.updateUser).Use(authMiddleware(s.jwtAuthenticator))
				usersWithID.DELETE("/", s.deleteUser)
			}
		}

		profiles := r.Group("/profiles").Use(authMiddleware(s.jwtAuthenticator))
		{
			profiles.POST("/", s.createProfile)
			profiles.GET("/", s.getProfile)
			profiles.PUT("/", s.updateProfile)
		}

		authorities := r.Group("/authorities").Use(authMiddleware(s.jwtAuthenticator))
		{
			authorities.POST("/", s.createAuthority)
			authorities.GET("/", s.listAuthorities)
			authorities.GET("/:id", s.getAuthorityById)
			authorities.DELETE("/:id", s.deleteAuthority)
		}

		activityTypes := r.Group("/activities").Use(authMiddleware(s.jwtAuthenticator))
		{
			activityTypes.GET("/", s.listActivityTypes)
			activityTypes.POST("/", s.createActivityType)
			activityTypes.PUT("/:id", s.updateActivityType)
			activityTypes.DELETE("/:id", s.archiveActivityType)
		}

		reports := r.Group("/reports").Use(authMiddleware(s.jwtAuthenticator))
		{
			reports.GET("/", s.listDailyReports)
			reports.GET("/summary", s.getReportSummary)
			reports.GET("/:date", s.getDailyReport)
			reports.PUT("/:date", s.saveDailyReport)
		}

		hymnBooks := r.Group("/hymns").Use(authMiddleware(s.jwtAuthenticator))
		{
			hymnBooks.GET("/books", s.listHymnBooks)
			hymnBooks.GET("/books/:code", s.getHymnBook)
			hymnBooks.GET("/books/:code/:number", s.getHymn)
		}
	}

	return router
}
