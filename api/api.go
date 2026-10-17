package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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
		slog.Warn("FIREBASE_CREDENTIALS_FILE is not set: the notifications reach the apps without push, and a forgotten password can't be reset")
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
	// the request id first: every line of a request carries it
	router := gin.New()
	router.Use(requestID(), logRequests(), recoverPanics())
	router.Use(cors.New(corsConfig(s.config.Server)))

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

			// our missionaries and our heroes
			admin.GET("/ministry/people", s.adminListMinistryPeople)
			admin.POST("/ministry/people", s.createMinistryPerson)
			admin.PUT("/ministry/people/:id", s.updateMinistryPerson)
			admin.DELETE("/ministry/people/:id", s.deleteMinistryPerson)
			// Nos documents: a PDF or a Word file each
			admin.GET("/documents", s.adminListDocuments)
			admin.POST("/documents/files", s.uploadDocumentFile)
			admin.POST("/documents", s.createDocument)
			admin.PUT("/documents/:id", s.updateDocument)
			admin.DELETE("/documents/:id", s.deleteDocument)
			// Enseignements: an MP3 or an M4A each
			admin.GET("/teachings", s.adminListTeachings)
			admin.POST("/teachings/files", s.uploadTeachingAudio)
			admin.POST("/teachings", s.createTeaching)
			admin.PUT("/teachings/:id", s.updateTeaching)
			admin.DELETE("/teachings/:id", s.deleteTeaching)

			// the hymn books: each change gives the book a new version
			admin.GET("/hymns/books", s.adminListHymnBooks)
			admin.POST("/hymns/books", s.createHymnBook)
			admin.PUT("/hymns/books/:code", s.updateHymnBook)
			admin.PUT("/hymns/books/:code/hymns/:number", s.saveHymn)
			admin.DELETE("/hymns/books/:code/hymns/:number", s.deleteHymn)

			// the standard items of the report, the same for every member
			admin.GET("/activity-types", s.adminListActivityTypes)
			admin.POST("/activity-types", s.adminCreateActivityType)
			admin.PUT("/activity-types/:id", s.adminUpdateActivityType)
			admin.PUT("/activity-types/:id/archive", s.adminArchiveActivityType)

			// the dashboard: counts only, never who
			admin.GET("/stats", s.adminStats)
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
		// the members who chose the signed-in member as one of their authorities,
		// and the reports they share
		r.GET("/disciples", authMiddleware(s.jwtAuthenticator), s.listDisciples)
		r.GET("/disciples/:id/reports", authMiddleware(s.jwtAuthenticator), s.listDiscipleReports)

		activityTypes := r.Group("/activities").Use(authMiddleware(s.jwtAuthenticator))
		{
			activityTypes.GET("/", s.listActivityTypes)
			activityTypes.POST("/", s.createActivityType)
			activityTypes.PUT("/:id", s.updateActivityType)
			activityTypes.DELETE("/:id", s.archiveActivityType)
			activityTypes.PUT("/:id/goal", s.setActivityGoal)
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
			ministry.GET("/people", s.listMinistryPeople)
		}

		documents := r.Group("/documents", authMiddleware(s.jwtAuthenticator))
		{
			documents.GET("", s.listDocuments)
			documents.GET("/categories", s.listDocumentCategories)
		}

		teachings := r.Group("/teachings", authMiddleware(s.jwtAuthenticator))
		{
			teachings.GET("", s.listTeachings)
			teachings.GET("/categories", s.listTeachingCategories)
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

// corsConfig lets the browsers of the allowed sites call the API (the web back
// office, on its own domain, with a Bearer token and no cookie). A request from
// any other site is refused (403); the apps send no Origin and aren't concerned.
func corsConfig(server utils.ServerConfig) cors.Config {
	allowed := make(map[string]bool, len(server.CORSOrigins))
	for _, origin := range server.CORSOrigins {
		allowed[strings.ToLower(strings.TrimSuffix(origin, "/"))] = true
	}
	// without CORS_ORIGINS: the local sites in development, none in release
	local := len(allowed) == 0 && server.GinMode != gin.ReleaseMode
	if len(allowed) == 0 && !local {
		slog.Warn("CORS_ORIGINS is not set: no browser on another site can call the API")
	}
	return cors.Config{
		AllowOriginFunc: func(origin string) bool {
			return allowed[strings.ToLower(origin)] || (local && isLocalOrigin(origin))
		},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "Accept-Language"},
		ExposeHeaders:    []string{"Content-Length", "Content-Disposition", requestIDHeader},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}
}

// isLocalOrigin tells whether a site runs on this machine:
// http://localhost or http://127.0.0.1, on any port.
func isLocalOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || "http://"+parsed.Host != origin {
		return false
	}
	host := parsed.Hostname()
	return host == "localhost" || host == "127.0.0.1"
}
