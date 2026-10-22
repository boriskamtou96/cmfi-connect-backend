package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	// the time zones of the places, even on a server without them
	_ "time/tzdata"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

const defaultTimeZone = "Africa/Douala"

func nullID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id != 0} }

func idPtr(id sql.NullInt64) *int64 {
	if !id.Valid {
		return nil
	}
	return &id.Int64
}

func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// registrationSummary: can one register, is the signed-in member registered,
// and how many people come.
type registrationSummary struct {
	// open, closed, full, ended or cancelled
	State         string `json:"state"`
	Registered    bool   `json:"registered"`
	Registrations int32  `json:"registrations"`
	People        int32  `json:"people"`
}

// eventSummaryResponse is an event in a list.
type eventSummaryResponse struct {
	ID           int64               `json:"id"`
	SeriesID     *int64              `json:"series_id"`
	Title        string              `json:"title"`
	Theme        string              `json:"theme"`
	StartsAt     time.Time           `json:"starts_at"`
	EndsAt       time.Time           `json:"ends_at"`
	TimeZone     string              `json:"time_zone"`
	Venue        string              `json:"venue"`
	City         string              `json:"city"`
	Country      string              `json:"country"`
	Poster       *mediaResponse      `json:"poster"`
	Status       string              `json:"status"`
	Registration registrationSummary `json:"registration"`
	// per person, paid by mobile money (POST /v1/events/:id/payments); null
	// for a free event
	Price *priceResponse `json:"price"`
}

type priceResponse struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

func priceOf(event db.Event) *priceResponse {
	if !event.PriceAmount.Valid {
		return nil
	}
	return &priceResponse{Amount: event.PriceAmount.Int64, Currency: event.PriceCurrency}
}

type sessionResponse struct {
	ID          int64           `json:"id"`
	StartsAt    time.Time       `json:"starts_at"`
	EndsAt      *time.Time      `json:"ends_at"`
	Title       string          `json:"title"`
	Speaker     string          `json:"speaker"`
	Place       string          `json:"place"`
	Description string          `json:"description"`
	Hymns       json.RawMessage `json:"hymns"`
}

type postResponse struct {
	ID        int64          `json:"id"`
	Body      string         `json:"body"`
	Image     *mediaResponse `json:"image"`
	Pinned    bool           `json:"pinned"`
	CreatedAt time.Time      `json:"created_at"`
}

type photoResponse struct {
	ID      int64         `json:"id"`
	Image   mediaResponse `json:"image"`
	Caption string        `json:"caption"`
}

type registrationResponse struct {
	People        int32     `json:"people"`
	NeedsLodging  bool      `json:"needs_lodging"`
	DepartureCity string    `json:"departure_city"`
	UpdatedAt     time.Time `json:"updated_at"`
	// made by a payment: its people can't change, and only a refund undoes it
	Paid bool `json:"paid"`
}

// editionResponse is another edition of the same yearly event.
type editionResponse struct {
	ID       int64          `json:"id"`
	Title    string         `json:"title"`
	StartsAt time.Time      `json:"starts_at"`
	EndsAt   time.Time      `json:"ends_at"`
	TimeZone string         `json:"time_zone"`
	Poster   *mediaResponse `json:"poster"`
}

// eventResponse is the whole event: what the event page shows, in one call
// (and kept by the app to show it without network).
type eventResponse struct {
	eventSummaryResponse
	ThemeVerse           string                `json:"theme_verse"`
	Description          string                `json:"description"`
	Address              string                `json:"address"`
	MapsURL              string                `json:"maps_url"`
	ContactName          string                `json:"contact_name"`
	ContactPhone         string                `json:"contact_phone"`
	RegistrationOpen     bool                  `json:"registration_open"`
	RegistrationDeadline *time.Time            `json:"registration_deadline"`
	Capacity             *int32                `json:"capacity"`
	AsksLodging          bool                  `json:"asks_lodging"`
	AsksDepartureCity    bool                  `json:"asks_departure_city"`
	FeeNote              string                `json:"fee_note"`
	Sessions             []sessionResponse     `json:"sessions"`
	Posts                []postResponse        `json:"posts"`
	Photos               []photoResponse       `json:"photos"`
	MyRegistration       *registrationResponse `json:"my_registration"`
	Editions             []editionResponse     `json:"editions"`
	// when the members were told by a push; null before
	NotifiedAt *time.Time `json:"notified_at"`
}

func (s *Server) eventSummaries(c *gin.Context, events []db.Event, userID int64) ([]eventSummaryResponse, error) {
	ids := make([]int64, len(events))
	posterIDs := make([]int64, len(events))
	for i, event := range events {
		ids[i] = event.ID
		posterIDs[i] = event.PosterID.Int64
	}
	posters, err := s.mediaByIDs(c, posterIDs)
	if err != nil {
		return nil, err
	}
	counts := map[int64]db.CountEventRegistrationsRow{}
	if len(ids) > 0 {
		rows, err := s.store.CountEventRegistrations(c, ids)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			counts[row.EventID] = row
		}
	}
	mine := map[int64]bool{}
	if len(ids) > 0 {
		rows, err := s.store.ListUserRegistrations(c, db.ListUserRegistrationsParams{UserID: userID, EventIds: ids})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			mine[row.EventID] = true
		}
	}

	now := time.Now()
	summaries := make([]eventSummaryResponse, 0, len(events))
	for _, event := range events {
		count := counts[event.ID]
		summary := eventSummaryResponse{
			ID:       event.ID,
			SeriesID: idPtr(event.SeriesID),
			Title:    event.Title,
			Theme:    event.Theme,
			StartsAt: event.StartsAt,
			EndsAt:   event.EndsAt,
			TimeZone: event.TimeZone,
			Venue:    event.Venue,
			City:     event.City,
			Country:  event.Country,
			Status:   event.Status,
			Price:    priceOf(event),
			Registration: registrationSummary{
				State:         db.RegistrationState(event, now, count.People),
				Registered:    mine[event.ID],
				Registrations: count.Registrations,
				People:        count.People,
			},
		}
		if poster, ok := posters[event.PosterID.Int64]; ok {
			summary.Poster = &poster
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func (s *Server) eventDetail(c *gin.Context, event db.Event, userID int64) (eventResponse, error) {
	summaries, err := s.eventSummaries(c, []db.Event{event}, userID)
	if err != nil {
		return eventResponse{}, err
	}
	response := eventResponse{
		eventSummaryResponse: summaries[0],
		NotifiedAt:           timePtr(event.NotifiedAt),
		ThemeVerse:           event.ThemeVerse,
		Description:          event.Description,
		Address:              event.Address,
		MapsURL:              event.MapsUrl,
		ContactName:          event.ContactName,
		ContactPhone:         event.ContactPhone,
		RegistrationOpen:     event.RegistrationOpen,
		RegistrationDeadline: timePtr(event.RegistrationDeadline),
		AsksLodging:          event.AsksLodging,
		AsksDepartureCity:    event.AsksDepartureCity,
		FeeNote:              event.FeeNote,
		Sessions:             []sessionResponse{},
		Posts:                []postResponse{},
		Photos:               []photoResponse{},
		Editions:             []editionResponse{},
	}
	if event.Capacity.Valid {
		response.Capacity = &event.Capacity.Int32
	}

	sessions, err := s.store.ListEventSessions(c, event.ID)
	if err != nil {
		return eventResponse{}, err
	}
	for _, session := range sessions {
		response.Sessions = append(response.Sessions, sessionResponse{
			ID:          session.ID,
			StartsAt:    session.StartsAt,
			EndsAt:      timePtr(session.EndsAt),
			Title:       session.Title,
			Speaker:     session.Speaker,
			Place:       session.Place,
			Description: session.Description,
			Hymns:       session.Hymns,
		})
	}

	posts, err := s.store.ListEventPosts(c, event.ID)
	if err != nil {
		return eventResponse{}, err
	}
	photos, err := s.store.ListEventPhotos(c, event.ID)
	if err != nil {
		return eventResponse{}, err
	}
	var editions []db.Event
	if event.SeriesID.Valid {
		all, err := s.store.ListEventSeriesEditions(c, event.SeriesID)
		if err != nil {
			return eventResponse{}, err
		}
		for _, edition := range all {
			if edition.ID != event.ID {
				editions = append(editions, edition)
			}
		}
	}
	mediaIDs := []int64{}
	for _, post := range posts {
		mediaIDs = append(mediaIDs, post.ImageID.Int64)
	}
	for _, photo := range photos {
		mediaIDs = append(mediaIDs, photo.MediaID)
	}
	for _, edition := range editions {
		mediaIDs = append(mediaIDs, edition.PosterID.Int64)
	}
	images, err := s.mediaByIDs(c, mediaIDs)
	if err != nil {
		return eventResponse{}, err
	}

	for _, post := range posts {
		item := postResponse{ID: post.ID, Body: post.Body, Pinned: post.Pinned, CreatedAt: post.CreatedAt}
		if image, ok := images[post.ImageID.Int64]; ok {
			item.Image = &image
		}
		response.Posts = append(response.Posts, item)
	}
	for _, photo := range photos {
		if image, ok := images[photo.MediaID]; ok {
			response.Photos = append(response.Photos, photoResponse{ID: photo.ID, Image: image, Caption: photo.Caption})
		}
	}
	for _, edition := range editions {
		item := editionResponse{
			ID: edition.ID, Title: edition.Title, StartsAt: edition.StartsAt, EndsAt: edition.EndsAt, TimeZone: edition.TimeZone,
		}
		if poster, ok := images[edition.PosterID.Int64]; ok {
			item.Poster = &poster
		}
		response.Editions = append(response.Editions, item)
	}

	registration, err := s.store.ListUserRegistrations(c, db.ListUserRegistrationsParams{UserID: userID, EventIds: []int64{event.ID}})
	if err != nil {
		return eventResponse{}, err
	}
	if len(registration) == 1 {
		response.MyRegistration = &registrationResponse{
			People:        registration[0].People,
			NeedsLodging:  registration[0].NeedsLodging,
			DepartureCity: registration[0].DepartureCity,
			UpdatedAt:     registration[0].UpdatedAt,
			Paid:          registration[0].PaymentID.Valid,
		}
	}
	return response, nil
}

// sendEvent answers the whole event, or the error that prevented it.
func (s *Server) sendEvent(c *gin.Context, status int, event db.Event) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	response, err := s.eventDetail(c, event, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, status, response)
}

type listEventsRequest struct {
	// upcoming (not over yet, soonest first) or past (latest first)
	When string `form:"when"`
	Page int32  `form:"page"`
	Size int32  `form:"size"`
}

func (r listEventsRequest) limits() (limit, offset int32) {
	size := r.Size
	if size <= 0 || size > 50 {
		size = 20
	}
	page := max(r.Page, 1)
	return size, (page - 1) * size
}

// GET /v1/events?when=upcoming|past&page=&size=
func (s *Server) listEvents(c *gin.Context) {
	var req listEventsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		badRequestError(c, err)
		return
	}
	limit, offset := req.limits()
	var events []db.Event
	var err error
	switch req.When {
	case "", "upcoming":
		events, err = s.store.ListUpcomingEvents(c, db.ListUpcomingEventsParams{Now: time.Now(), Lim: limit, Off: offset})
	case "past":
		events, err = s.store.ListPastEvents(c, db.ListPastEventsParams{Now: time.Now(), Lim: limit, Off: offset})
	default:
		badRequestError(c, fmt.Errorf("when must be upcoming or past"))
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	summaries, err := s.eventSummaries(c, events, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, summaries)
}

type eventIDRequest struct {
	ID int64 `uri:"id" binding:"required"`
}

// loadEvent finds the event of the URL. A draft is only for the admins:
// for the members it doesn't exist.
func (s *Server) loadEvent(c *gin.Context, allowDraft bool) (db.Event, bool) {
	var req eventIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return db.Event{}, false
	}
	event, err := s.store.GetEvent(c, req.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Event")
		} else {
			internalServerError(c, err)
		}
		return db.Event{}, false
	}
	if event.Status == "draft" && !allowDraft && !s.isStaff(c) {
		resourceNotFoundError(c, "Event")
		return db.Event{}, false
	}
	return event, true
}

// isStaff: the signed-in member is an admin or a superadmin.
func (s *Server) isStaff(c *gin.Context) bool {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	role, err := s.store.GetUserRole(c, authUser.ID)
	return err == nil && (role == roleAdmin || role == roleSuperadmin)
}

// GET /v1/events/:id
func (s *Server) getEvent(c *gin.Context) {
	event, ok := s.loadEvent(c, false)
	if !ok {
		return
	}
	s.sendEvent(c, http.StatusOK, event)
}

type registrationRequest struct {
	People        int32  `json:"people"`
	NeedsLodging  bool   `json:"needs_lodging"`
	DepartureCity string `json:"departure_city"`
}

// PUT /v1/events/:id/registration: registers the signed-in member, or changes
// their registration. Answers the whole event.
func (s *Server) registerForEvent(c *gin.Context) {
	event, ok := s.loadEvent(c, false)
	if !ok {
		return
	}
	var req registrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if req.People == 0 {
		req.People = 1
	}
	if req.People < 1 || req.People > 50 {
		badRequestError(c, fmt.Errorf("people: 1 to 50"))
		return
	}
	// only the questions the event asks are kept
	if !event.AsksLodging {
		req.NeedsLodging = false
	}
	if !event.AsksDepartureCity {
		req.DepartureCity = ""
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	// a paid event: one may register now and pay later (POST
	// /v1/events/:id/payments); once paid, only the lodging and the city
	// change
	if event.PriceAmount.Valid {
		registration, err := s.store.GetEventRegistration(c, db.GetEventRegistrationParams{EventID: event.ID, UserID: authUser.ID})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			internalServerError(c, err)
			return
		}
		if err == nil && registration.PaymentID.Valid && registration.People != req.People {
			c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "PEOPLE_PAID",
				fmt.Errorf("the people of a paid registration can't change")))
			return
		}
	}
	_, err := s.store.RegisterForEventTx(c, db.UpsertEventRegistrationParams{
		EventID:       event.ID,
		UserID:        authUser.ID,
		People:        req.People,
		NeedsLodging:  req.NeedsLodging,
		DepartureCity: strings.TrimSpace(req.DepartureCity),
	}, time.Now())
	switch {
	case errors.Is(err, db.ErrEventNotFound):
		resourceNotFoundError(c, "Event")
		return
	case errors.Is(err, db.ErrEventFull):
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "EVENT_FULL", err))
		return
	case errors.Is(err, db.ErrRegistrationClosed):
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "REGISTRATION_CLOSED", err))
		return
	case err != nil:
		internalServerError(c, err)
		return
	}
	s.sendEvent(c, http.StatusOK, event)
}

// DELETE /v1/events/:id/registration: the signed-in member doesn't come any more.
func (s *Server) unregisterFromEvent(c *gin.Context) {
	event, ok := s.loadEvent(c, false)
	if !ok {
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	// paid: undone by a refund (the organizers)
	registration, err := s.store.GetEventRegistration(c, db.GetEventRegistrationParams{EventID: event.ID, UserID: authUser.ID})
	if err == nil && registration.PaymentID.Valid {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "PAID_REGISTRATION",
			fmt.Errorf("a paid registration is cancelled by the organizers, with a refund")))
		return
	}
	if _, err := s.store.DeleteEventRegistration(c, db.DeleteEventRegistrationParams{EventID: event.ID, UserID: authUser.ID}); err != nil {
		internalServerError(c, err)
		return
	}
	s.sendEvent(c, http.StatusOK, event)
}

// ----- admins -----

// GET /v1/admin/events?page=&size=: every event, drafts included, latest first.
func (s *Server) adminListEvents(c *gin.Context) {
	var req listEventsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		badRequestError(c, err)
		return
	}
	limit, offset := req.limits()
	events, err := s.store.ListAllEvents(c, db.ListAllEventsParams{Lim: limit, Off: offset})
	if err != nil {
		internalServerError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	summaries, err := s.eventSummaries(c, events, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, summaries)
}

type eventRequest struct {
	Title                string     `json:"title" binding:"required"`
	Theme                string     `json:"theme"`
	ThemeVerse           string     `json:"theme_verse"`
	Description          string     `json:"description"`
	StartsAt             time.Time  `json:"starts_at" binding:"required"`
	EndsAt               time.Time  `json:"ends_at" binding:"required"`
	TimeZone             string     `json:"time_zone"`
	Venue                string     `json:"venue"`
	Address              string     `json:"address"`
	City                 string     `json:"city"`
	Country              string     `json:"country"`
	MapsURL              string     `json:"maps_url"`
	ContactName          string     `json:"contact_name"`
	ContactPhone         string     `json:"contact_phone"`
	PosterID             *int64     `json:"poster_id"`
	RegistrationOpen     *bool      `json:"registration_open"`
	RegistrationDeadline *time.Time `json:"registration_deadline"`
	Capacity             *int32     `json:"capacity"`
	AsksLodging          bool       `json:"asks_lodging"`
	AsksDepartureCity    bool       `json:"asks_departure_city"`
	FeeNote              string     `json:"fee_note"`
	// the price per person; null or 0 for a free event
	PriceAmount   *int64 `json:"price_amount"`
	PriceCurrency string `json:"price_currency"`
	// draft (default), published or cancelled
	Status string `json:"status"`
	// « Prévenir les membres » the first time it is published (true when
	// absent)
	Notify *bool `json:"notify"`
}

// params checks the request and turns it into the columns of an event.
func (s *Server) eventParams(c *gin.Context, req eventRequest) (db.CreateEventParams, error) {
	params := db.CreateEventParams{
		Title:             strings.TrimSpace(req.Title),
		Theme:             strings.TrimSpace(req.Theme),
		ThemeVerse:        strings.TrimSpace(req.ThemeVerse),
		Description:       strings.TrimSpace(req.Description),
		StartsAt:          req.StartsAt,
		EndsAt:            req.EndsAt,
		TimeZone:          req.TimeZone,
		Venue:             strings.TrimSpace(req.Venue),
		Address:           strings.TrimSpace(req.Address),
		City:              strings.TrimSpace(req.City),
		Country:           strings.TrimSpace(req.Country),
		MapsUrl:           strings.TrimSpace(req.MapsURL),
		ContactName:       strings.TrimSpace(req.ContactName),
		ContactPhone:      strings.TrimSpace(req.ContactPhone),
		RegistrationOpen:  req.RegistrationOpen == nil || *req.RegistrationOpen,
		AsksLodging:       req.AsksLodging,
		AsksDepartureCity: req.AsksDepartureCity,
		FeeNote:           strings.TrimSpace(req.FeeNote),
		Status:            req.Status,
		PriceCurrency:     strings.ToUpper(strings.TrimSpace(req.PriceCurrency)),
	}
	if params.Title == "" {
		return params, fmt.Errorf("title is required")
	}
	if params.EndsAt.Before(params.StartsAt) {
		return params, fmt.Errorf("ends_at is before starts_at")
	}
	if params.TimeZone == "" {
		params.TimeZone = defaultTimeZone
	}
	if _, err := time.LoadLocation(params.TimeZone); err != nil {
		return params, fmt.Errorf("unknown time_zone %q", params.TimeZone)
	}
	if params.Status == "" {
		params.Status = "draft"
	}
	if !slices.Contains([]string{"draft", "published", "cancelled"}, params.Status) {
		return params, fmt.Errorf("status must be draft, published or cancelled")
	}
	if req.Capacity != nil {
		if *req.Capacity <= 0 {
			return params, fmt.Errorf("capacity must be positive")
		}
		params.Capacity = sql.NullInt32{Int32: *req.Capacity, Valid: true}
	}
	if params.PriceCurrency == "" {
		params.PriceCurrency = "XAF"
	}
	if len(params.PriceCurrency) != 3 || strings.ToUpper(params.PriceCurrency) != params.PriceCurrency {
		return params, fmt.Errorf("price_currency must be a currency code (XAF)")
	}
	if req.PriceAmount != nil {
		if *req.PriceAmount < 0 {
			return params, fmt.Errorf("price_amount must be positive")
		}
		params.PriceAmount = sql.NullInt64{Int64: *req.PriceAmount, Valid: *req.PriceAmount > 0}
	}
	if req.RegistrationDeadline != nil {
		params.RegistrationDeadline = sql.NullTime{Time: *req.RegistrationDeadline, Valid: true}
	}
	if req.PosterID != nil && *req.PosterID != 0 {
		found, err := s.mediaByIDs(c, []int64{*req.PosterID})
		if err != nil {
			return params, err
		}
		if _, ok := found[*req.PosterID]; !ok {
			return params, fmt.Errorf("unknown poster_id %d", *req.PosterID)
		}
		params.PosterID = nullID(*req.PosterID)
	}
	return params, nil
}

// POST /v1/admin/events
func (s *Server) createEvent(c *gin.Context) {
	var req eventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	params, err := s.eventParams(c, req)
	if err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	params.CreatedBy = nullID(authUser.ID)
	event, err := s.store.CreateEvent(c, params)
	if err != nil {
		internalServerError(c, err)
		return
	}
	s.sendEvent(c, http.StatusCreated, s.announcedEvent(c, event, req.Notify))
}

// PUT /v1/admin/events/:id: the whole event (not its program, news nor photos).
func (s *Server) updateEvent(c *gin.Context) {
	event, ok := s.loadEvent(c, true)
	if !ok {
		return
	}
	var req eventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	params, err := s.eventParams(c, req)
	if err != nil {
		badRequestError(c, err)
		return
	}
	updated, err := s.store.UpdateEvent(c, db.UpdateEventParams{
		ID:                   event.ID,
		Title:                params.Title,
		Theme:                params.Theme,
		ThemeVerse:           params.ThemeVerse,
		Description:          params.Description,
		StartsAt:             params.StartsAt,
		EndsAt:               params.EndsAt,
		TimeZone:             params.TimeZone,
		Venue:                params.Venue,
		Address:              params.Address,
		City:                 params.City,
		Country:              params.Country,
		MapsUrl:              params.MapsUrl,
		ContactName:          params.ContactName,
		ContactPhone:         params.ContactPhone,
		PosterID:             params.PosterID,
		RegistrationOpen:     params.RegistrationOpen,
		RegistrationDeadline: params.RegistrationDeadline,
		Capacity:             params.Capacity,
		AsksLodging:          params.AsksLodging,
		AsksDepartureCity:    params.AsksDepartureCity,
		FeeNote:              params.FeeNote,
		Status:               params.Status,
		PriceAmount:          params.PriceAmount,
		PriceCurrency:        params.PriceCurrency,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	s.sendEvent(c, http.StatusOK, s.announcedEvent(c, updated, req.Notify))
}

// DELETE /v1/admin/events/:id: with its program, registrations, news and
// photos (the images stay in the media storage).
func (s *Server) deleteEvent(c *gin.Context) {
	event, ok := s.loadEvent(c, true)
	if !ok {
		return
	}
	// the money of its members is refunded first
	held, err := s.store.CountHeldEventPayments(c, nullID(event.ID))
	if err != nil {
		internalServerError(c, err)
		return
	}
	if held > 0 {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "EVENT_HAS_PAYMENTS",
			fmt.Errorf("%d payments to refund first", held)))
		return
	}
	if _, err := s.store.DeleteEvent(c, event.ID); err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusNoContent, nil)
}

type hymnRef struct {
	Book   string `json:"book"`
	Number int32  `json:"number"`
}

type sessionRequest struct {
	StartsAt    time.Time  `json:"starts_at" binding:"required"`
	EndsAt      *time.Time `json:"ends_at"`
	Title       string     `json:"title" binding:"required"`
	Speaker     string     `json:"speaker"`
	Place       string     `json:"place"`
	Description string     `json:"description"`
	Hymns       []hymnRef  `json:"hymns"`
}

type sessionsRequest struct {
	Sessions []sessionRequest `json:"sessions" binding:"dive"`
}

// PUT /v1/admin/events/:id/sessions: replaces the whole program.
func (s *Server) replaceEventSessions(c *gin.Context) {
	event, ok := s.loadEvent(c, true)
	if !ok {
		return
	}
	var req sessionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	sessions := make([]db.CreateEventSessionParams, 0, len(req.Sessions))
	for i, session := range req.Sessions {
		title := strings.TrimSpace(session.Title)
		if title == "" {
			badRequestError(c, fmt.Errorf("sessions[%d]: title is required", i))
			return
		}
		endsAt := sql.NullTime{}
		if session.EndsAt != nil {
			if session.EndsAt.Before(session.StartsAt) {
				badRequestError(c, fmt.Errorf("sessions[%d]: ends_at is before starts_at", i))
				return
			}
			endsAt = sql.NullTime{Time: *session.EndsAt, Valid: true}
		}
		hymns := session.Hymns
		if hymns == nil {
			hymns = []hymnRef{}
		}
		for _, hymn := range hymns {
			if hymn.Book == "" || hymn.Number <= 0 {
				badRequestError(c, fmt.Errorf("sessions[%d]: a hymn needs a book and a number", i))
				return
			}
		}
		hymnsJSON, _ := json.Marshal(hymns)
		sessions = append(sessions, db.CreateEventSessionParams{
			StartsAt:    session.StartsAt,
			EndsAt:      endsAt,
			Title:       title,
			Speaker:     strings.TrimSpace(session.Speaker),
			Place:       strings.TrimSpace(session.Place),
			Description: strings.TrimSpace(session.Description),
			Hymns:       hymnsJSON,
		})
	}
	if _, err := s.store.ReplaceEventSessionsTx(c, event.ID, sessions); err != nil {
		internalServerError(c, err)
		return
	}
	s.sendEvent(c, http.StatusOK, event)
}

// POST /v1/admin/events/:id/duplicate: the next edition, as a draft one year later.
func (s *Server) duplicateEvent(c *gin.Context) {
	event, ok := s.loadEvent(c, true)
	if !ok {
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	next, err := s.store.DuplicateEventTx(c, event.ID, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	s.sendEvent(c, http.StatusCreated, next)
}

type registrantResponse struct {
	memberCard
	PhoneNumber   string    `json:"phone_number"`
	People        int32     `json:"people"`
	NeedsLodging  bool      `json:"needs_lodging"`
	DepartureCity string    `json:"departure_city"`
	CreatedAt     time.Time `json:"created_at"`
	// a paid event: registered by a payment (else to pay, or free)
	Paid bool `json:"paid"`
}

type cityCount struct {
	City   string `json:"city"`
	People int32  `json:"people"`
}

type registrationsResponse struct {
	Registrations int32 `json:"registrations"`
	People        int32 `json:"people"`
	Lodging       int32 `json:"lodging"`
	// the people whose registration is paid (a paid event)
	PaidPeople  int32                `json:"paid_people"`
	Cities      []cityCount          `json:"cities"`
	Registrants []registrantResponse `json:"registrants"`
}

// GET /v1/admin/events/:id/registrations: who comes, with the totals the
// organizers need (people, beds, departure cities).
func (s *Server) listEventRegistrations(c *gin.Context) {
	event, ok := s.loadEvent(c, true)
	if !ok {
		return
	}
	rows, err := s.store.ListEventRegistrations(c, event.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.UserID
	}
	cards := map[int64]db.ListMemberCardsRow{}
	if len(ids) > 0 {
		found, err := s.store.ListMemberCards(c, ids)
		if err != nil {
			internalServerError(c, err)
			return
		}
		for _, card := range found {
			cards[card.ID] = card
		}
	}

	response := registrationsResponse{Registrants: []registrantResponse{}, Cities: []cityCount{}}
	byCity := map[string]int32{}
	for _, row := range rows {
		card := cards[row.UserID]
		response.Registrations++
		response.People += row.People
		if row.NeedsLodging {
			response.Lodging += row.People
		}
		if row.DepartureCity != "" {
			byCity[row.DepartureCity] += row.People
		}
		if row.PaymentID.Valid {
			response.PaidPeople += row.People
		}
		response.Registrants = append(response.Registrants, registrantResponse{
			Paid:          row.PaymentID.Valid,
			memberCard:    toMemberCard(card),
			PhoneNumber:   card.PhoneNumber,
			People:        row.People,
			NeedsLodging:  row.NeedsLodging,
			DepartureCity: row.DepartureCity,
			CreatedAt:     row.CreatedAt,
		})
	}
	for city, people := range byCity {
		response.Cities = append(response.Cities, cityCount{City: city, People: people})
	}
	slices.SortFunc(response.Cities, func(a, b cityCount) int {
		if a.People != b.People {
			return int(b.People - a.People)
		}
		return strings.Compare(a.City, b.City)
	})
	apiResponse(c, http.StatusOK, response)
}

type postRequest struct {
	Body    string `json:"body" binding:"required"`
	ImageID *int64 `json:"image_id"`
	Pinned  bool   `json:"pinned"`
}

// POST /v1/admin/events/:id/posts: a news of the event. Answers the whole event.
func (s *Server) createEventPost(c *gin.Context) {
	event, ok := s.loadEvent(c, true)
	if !ok {
		return
	}
	var req postRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		badRequestError(c, fmt.Errorf("body is required"))
		return
	}
	imageID := sql.NullInt64{}
	if req.ImageID != nil && *req.ImageID != 0 {
		found, err := s.mediaByIDs(c, []int64{*req.ImageID})
		if err != nil {
			internalServerError(c, err)
			return
		}
		if _, ok := found[*req.ImageID]; !ok {
			badRequestError(c, fmt.Errorf("unknown image_id %d", *req.ImageID))
			return
		}
		imageID = nullID(*req.ImageID)
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	_, err := s.store.CreateEventPost(c, db.CreateEventPostParams{
		EventID:  event.ID,
		Body:     body,
		ImageID:  imageID,
		Pinned:   req.Pinned,
		AuthorID: nullID(authUser.ID),
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	s.sendEvent(c, http.StatusCreated, event)
}

type eventChildRequest struct {
	ID      int64 `uri:"id" binding:"required"`
	ChildID int64 `uri:"child" binding:"required"`
}

// DELETE /v1/admin/events/:id/posts/:child
func (s *Server) deleteEventPost(c *gin.Context) {
	var req eventChildRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return
	}
	deleted, err := s.store.DeleteEventPost(c, db.DeleteEventPostParams{ID: req.ChildID, EventID: req.ID})
	if err != nil {
		internalServerError(c, err)
		return
	}
	if deleted == 0 {
		resourceNotFoundError(c, "Post")
		return
	}
	apiResponse(c, http.StatusNoContent, nil)
}

type photosRequest struct {
	MediaIDs []int64 `json:"media_ids" binding:"required,min=1"`
	Caption  string  `json:"caption"`
}

// POST /v1/admin/events/:id/photos: adds images (sent first to /admin/media)
// to the gallery. Answers the whole event.
func (s *Server) addEventPhotos(c *gin.Context) {
	event, ok := s.loadEvent(c, true)
	if !ok {
		return
	}
	var req photosRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	found, err := s.mediaByIDs(c, req.MediaIDs)
	if err != nil {
		internalServerError(c, err)
		return
	}
	for _, id := range req.MediaIDs {
		if _, ok := found[id]; !ok {
			badRequestError(c, fmt.Errorf("unknown media_id %d", id))
			return
		}
	}
	for _, id := range req.MediaIDs {
		_, err := s.store.CreateEventPhoto(c, db.CreateEventPhotoParams{
			EventID: event.ID,
			MediaID: id,
			Caption: strings.TrimSpace(req.Caption),
		})
		if err != nil {
			internalServerError(c, err)
			return
		}
	}
	s.sendEvent(c, http.StatusCreated, event)
}

// DELETE /v1/admin/events/:id/photos/:child
func (s *Server) deleteEventPhoto(c *gin.Context) {
	var req eventChildRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return
	}
	deleted, err := s.store.DeleteEventPhoto(c, db.DeleteEventPhotoParams{ID: req.ChildID, EventID: req.ID})
	if err != nil {
		internalServerError(c, err)
		return
	}
	if deleted == 0 {
		resourceNotFoundError(c, "Photo")
		return
	}
	apiResponse(c, http.StatusNoContent, nil)
}
