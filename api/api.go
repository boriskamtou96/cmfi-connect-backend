package api

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/media"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/phoneauth"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/push"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

type Server struct {
	store            *db.SQLStore
	config           *utils.Config
	jwtAuthenticator token.JWTAuthenticator
	media            media.Store
	push             push.Sender
	// checks the SMS code of "forgot password" (Firebase Authentication)
	phones phoneauth.Verifier
}

func New(store *db.SQLStore, cfg *utils.Config) (*Server, error) {
	jwtAuthenticator, err := token.NewPasetoMaker(cfg.Token.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("token.NewPasetoMaker: %w", err)
	}

	// tests build a config by hand: their images go to a temporary folder
	mediaDir := cfg.Media.Dir
	if mediaDir == "" {
		mediaDir = filepath.Join(os.TempDir(), "cmfi-connect-media")
	}
	mediaStore, err := media.NewStore(mediaDir, cfg.Media.S3())
	if err != nil {
		return nil, err
	}
	mediaPrefix = apiMediaPrefix
	if public := strings.TrimSuffix(cfg.Media.PublicURL, "/"); public != "" {
		mediaPrefix = public + "/"
	}

	var sender push.Sender = push.Disabled{}
	var phones phoneauth.Verifier = phoneauth.Disabled{}
	if cfg.Push.CredentialsFile != "" {
		firebaseSender, err := push.NewFirebase(context.Background(), cfg.Push.CredentialsFile)
		if err != nil {
			return nil, err
		}
		sender = firebaseSender
		firebasePhones, err := phoneauth.NewFirebase(context.Background(), cfg.Push.CredentialsFile)
		if err != nil {
			return nil, err
		}
		phones = firebasePhones
	} else {
		log.Println("FIREBASE_CREDENTIALS_FILE is not set: the notifications reach the apps without push, and a forgotten password can't be reset")
	}

	return &Server{
		store:            store,
		config:           cfg,
		jwtAuthenticator: jwtAuthenticator,
		media:            mediaStore,
		push:             sender,
		phones:           phones,
	}, nil
}

func (s *Server) SetupRouter() *gin.Engine {
	router := gin.Default()
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{},
		AllowMethods:     []string{"PUT", "PATCH"},
		AllowHeaders:     []string{"Origin"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		AllowOriginFunc: func(origin string) bool {
			return true
		},
		MaxAge: 12 * time.Hour,
	}))

	router.NoRoute(func(c *gin.Context) {
		resourceNotFoundError(c, "ROUTE")
	})

	router.NoMethod(func(c *gin.Context) {
		methodNotAllowError(c)
	})

	r := router.Group("v1")
	{
		// Ops
		r.GET("/health", s.health)

		users := r.Group("users")
		{
			users.POST("/register", s.registerUser)
			users.POST("/login", s.login)
			// forgot password: proven with an SMS code (Firebase)
			users.POST("/password/reset", s.resetPassword)
		}
		// every other account route needs to be signed in, and acts on one's
		// own account (admins may read any, superadmins may delete any)
		signedInUsers := r.Group("users", authMiddleware(s.jwtAuthenticator))
		{
			signedInUsers.GET("/me", s.getMe)
			signedInUsers.DELETE("/me", s.deleteMe)
			signedInUsers.PUT("/me/username", s.updateMyUsername)
			signedInUsers.PUT("/me/avatar", s.uploadMyAvatar)
			signedInUsers.DELETE("/me/avatar", s.deleteMyAvatar)
			signedInUsers.GET("/by-username/:username", s.getMemberByUsername)
			usersWithID := signedInUsers.Group("/:id")
			{
				usersWithID.GET("/", s.getUser)
				usersWithID.PUT("/", s.updateUser)
				usersWithID.DELETE("/", s.deleteUser)
			}
		}

		// for the sign-up form: no account yet
		r.GET("/usernames/availability", s.usernameAvailability)

		// a member blocks or reports another one (invitations to pray)
		members := r.Group("/members", authMiddleware(s.jwtAuthenticator))
		{
			members.GET("/blocked", s.listBlockedMembers)
			members.POST("/:username/block", s.blockMember)
			members.DELETE("/:username/block", s.unblockMember)
		}
		r.POST("/reports", authMiddleware(s.jwtAuthenticator), s.createReport)

		admin := r.Group("/admin", authMiddleware(s.jwtAuthenticator), requireRole(s.store, roleAdmin, roleSuperadmin))
		{
			admin.GET("/staff", s.listStaff)
			admin.GET("/members", s.listAdminMembers)
			admin.GET("/reports", s.adminListReports)
			admin.PUT("/reports/:id/resolve", s.resolveReport)
			admin.GET("/members/:id", s.getAdminMember)
			// moderation: a photo that has no place in the community
			admin.DELETE("/members/:id/avatar", s.adminDeleteMemberAvatar)
			admin.PUT("/members/:username/role", requireRole(s.store, roleSuperadmin), s.setMemberRole)

			admin.POST("/media", s.uploadMedia)
			admin.GET("/events", s.adminListEvents)
			admin.POST("/events", s.createEvent)
			admin.PUT("/events/:id", s.updateEvent)
			admin.DELETE("/events/:id", s.deleteEvent)
			admin.PUT("/events/:id/sessions", s.replaceEventSessions)
			admin.POST("/events/:id/duplicate", s.duplicateEvent)
			admin.GET("/events/:id/registrations", s.listEventRegistrations)
			admin.POST("/events/:id/posts", s.createEventPost)
			admin.DELETE("/events/:id/posts/:child", s.deleteEventPost)
			admin.POST("/events/:id/photos", s.addEventPhotos)
			admin.DELETE("/events/:id/photos/:child", s.deleteEventPhoto)

			admin.GET("/prayer/topics", s.adminListPrayerTopics)
			admin.POST("/prayer/topics", s.createPrayerTopic)
			admin.PUT("/prayer/topics/:id", s.updatePrayerTopic)
			admin.DELETE("/prayer/topics/:id", s.deletePrayerTopic)

			admin.GET("/notifications/audience", s.notificationAudience)
			admin.GET("/notifications", s.adminListNotifications)
			admin.POST("/notifications", s.sendNotification)
			admin.GET("/notifications/:id", s.adminGetNotification)

			admin.POST("/ministry/nations", s.createNation)
			admin.PUT("/ministry/nations/:id", s.updateNation)
			admin.DELETE("/ministry/nations/:id", s.deleteNation)
		}

		// the images of the events: public links with random names
		r.GET("/media/:key", s.serveMedia)

		events := r.Group("/events", authMiddleware(s.jwtAuthenticator))
		{
			events.GET("/", s.listEvents)
			events.GET("/:id", s.getEvent)
			events.PUT("/:id/registration", s.registerForEvent)
			events.DELETE("/:id/registration", s.unregisterFromEvent)
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
			authorities.GET("/requests", s.listAuthorityRequests)
			authorities.POST("/requests/:id/accept", s.answerAuthorityRequest(authorityAccepted))
			authorities.POST("/requests/:id/decline", s.answerAuthorityRequest(authorityDeclined))
			authorities.GET("/:id", s.getAuthorityById)
			authorities.DELETE("/:id", s.deleteAuthority)
			authorities.PUT("/:id/sharing", s.setAuthoritySharing)
		}
		// the members who chose the signed-in member as their disciple maker
		r.GET("/disciples", authMiddleware(s.jwtAuthenticator), s.listDisciples)

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

		// the prayer module: topics of the community, own lists, retreats
		// and importunity topics
		prayer := r.Group("/prayer", authMiddleware(s.jwtAuthenticator))
		{
			prayer.GET("/topics", s.listPrayerTopics)
			prayer.GET("/topics/:id", s.getPrayerTopic)
			prayer.PUT("/topics/:id/prayed", s.setPrayedPoints)

			prayer.GET("/people", s.listPrayerPeople)
			prayer.POST("/people", s.createPrayerPerson)
			prayer.POST("/people/prayed", s.markPrayerPeoplePrayed)
			prayer.PUT("/people/:id", s.updatePrayerPerson)
			prayer.DELETE("/people/:id", s.deletePrayerPerson)

			prayer.GET("/retreats", s.listPrayerRetreats)
			prayer.PUT("/retreats/:day/:moment", s.savePrayerRetreat)
			prayer.DELETE("/retreats/:day/:moment", s.deletePrayerRetreat)

			prayer.GET("/importunities", s.listImportunities)
			prayer.POST("/importunities", s.createImportunity)
			prayer.GET("/importunities/family", s.getFamily)
			prayer.GET("/importunities/:id", s.getImportunity)
			prayer.PUT("/importunities/:id", s.updateImportunity)
			prayer.DELETE("/importunities/:id", s.deleteImportunity)
			prayer.POST("/importunities/:id/count", s.countProclamations)
			prayer.POST("/importunities/:id/invitations", s.inviteToImportunity)
			prayer.PUT("/importunities/:id/answer", s.answerImportunity)
			prayer.DELETE("/importunities/:id/membership", s.leaveImportunity)
		}

		// our ministry: the nations where it is present
		ministry := r.Group("/ministry", authMiddleware(s.jwtAuthenticator))
		{
			ministry.GET("/nations", s.listNations)
		}

		// the phones that receive push notifications, and the member's
		// notifications (the announcements of the admins)
		devices := r.Group("/devices", authMiddleware(s.jwtAuthenticator))
		{
			devices.PUT("", s.registerDevice)
			devices.DELETE("/:token", s.unregisterDevice)
		}
		notifications := r.Group("/notifications", authMiddleware(s.jwtAuthenticator))
		{
			notifications.GET("", s.listMyNotifications)
			notifications.GET("/:id", s.getMyNotification)
			notifications.PUT("/:id/read", s.readNotification)
		}
	}

	return router
}
