package api

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/push"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

const (
	maxNotificationTitle       = 120
	maxNotificationBody        = 4000
	maxNotificationAttachments = 10
	// the text of the push: the phone shows the start, the app the rest
	maxPushBody = 200
	pushTimeout = 25 * time.Second
)

// ----- phones -----

type deviceRequest struct {
	Token    string `json:"token" binding:"required,max=4096"`
	Platform string `json:"platform" binding:"required,oneof=android ios"`
	// the app language ("fr"), for the texts to come
	Language string `json:"language" binding:"omitempty,min=2,max=10"`
}

// PUT /v1/devices: the phone of the signed-in member receives the push. A
// token known for another member moves to this one (same phone, new account).
func (s *Server) registerDevice(c *gin.Context) {
	var req deviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if _, err := s.store.UpsertDevice(c, db.UpsertDeviceParams{
		UserID:   authUser.ID,
		Token:    req.Token,
		Platform: req.Platform,
		Language: cmp.Or(req.Language, "fr"),
	}); err != nil {
		internalServerError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type deviceTokenRequest struct {
	Token string `uri:"token" binding:"required"`
}

// DELETE /v1/devices/:token: signing out, the phone stops receiving the push.
func (s *Server) unregisterDevice(c *gin.Context) {
	var req deviceTokenRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if _, err := s.store.DeleteDevice(c, db.DeleteDeviceParams{Token: req.Token, UserID: authUser.ID}); err != nil {
		internalServerError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ----- who receives an announcement -----

// audienceFilter: the members whose profile has these values; an empty
// field doesn't filter, all empty is everyone.
type audienceFilter struct {
	Country  string `json:"country" form:"country"`
	City     string `json:"city" form:"city"`
	Church   string `json:"church" form:"church"`
	Assembly string `json:"assembly" form:"assembly"`
}

func (f audienceFilter) trimmed() audienceFilter {
	return audienceFilter{
		Country:  strings.ToUpper(strings.TrimSpace(f.Country)),
		City:     strings.TrimSpace(f.City),
		Church:   strings.TrimSpace(f.Church),
		Assembly: strings.TrimSpace(f.Assembly),
	}
}

// The places of a profile, in the order of the filters.
const (
	placeCountry = iota
	placeCity
	placeChurch
	placeAssembly
	placeCount
)

func (f audienceFilter) places() [placeCount]string {
	return [placeCount]string{f.Country, f.City, f.Church, f.Assembly}
}

func memberPlaces(m db.ListAudienceMembersRow) [placeCount]string {
	return [placeCount]string{m.Country, m.City, m.Church, m.Assembly}
}

var accents = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a",
	"ç", "c", "è", "e", "é", "e", "ê", "e", "ë", "e",
	"ì", "i", "í", "i", "î", "i", "ï", "i", "ñ", "n",
	"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o",
	"ù", "u", "ú", "u", "û", "u", "ü", "u", "ý", "y", "ÿ", "y",
	"œ", "oe", "æ", "ae", "’", "'",
)

var spaces = regexp.MustCompile(`[\s\-_]+`)

// fold makes the values typed by hand comparable: "Yaoundé " and "yaounde",
// "Biyem-Assi" and "biyem assi" are the same place.
func fold(value string) string {
	return spaces.ReplaceAllString(accents.Replace(strings.ToLower(strings.TrimSpace(value))), " ")
}

var countryCode = regexp.MustCompile(`^[A-Z]{2}$`)

// matches tells whether a member is in the audience, leaving out the place
// [skip] (-1 checks them all).
func matches(member, wanted [placeCount]string, skip int) bool {
	for place := range placeCount {
		if place == skip || wanted[place] == "" {
			continue
		}
		if fold(member[place]) != fold(wanted[place]) {
			return false
		}
	}
	return true
}

func (s *Server) audienceOf(c *gin.Context, filter audienceFilter) ([]db.ListAudienceMembersRow, []int64, error) {
	members, err := s.store.ListAudienceMembers(c)
	if err != nil {
		return nil, nil, err
	}
	wanted := filter.places()
	ids := []int64{}
	for _, member := range members {
		if matches(memberPlaces(member), wanted, -1) {
			ids = append(ids, member.ID)
		}
	}
	return members, ids, nil
}

type audienceValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type audienceResponse struct {
	Recipients int             `json:"recipients"`
	Phones     int32           `json:"phones"`
	Countries  []audienceValue `json:"countries"`
	Cities     []audienceValue `json:"cities"`
	Churches   []audienceValue `json:"churches"`
	Assemblies []audienceValue `json:"assemblies"`
}

// valuesOf lists the values of [place] among the members matching the other
// filters: grouped by [fold], shown with their most frequent spelling.
func valuesOf(members []db.ListAudienceMembersRow, wanted [placeCount]string, place int) []audienceValue {
	type group struct {
		count     int
		spellings map[string]int
	}
	groups := map[string]*group{}
	for _, member := range members {
		places := memberPlaces(member)
		if !matches(places, wanted, place) {
			continue
		}
		value := strings.TrimSpace(places[place])
		if place == placeCountry {
			// a code since the list of countries; an old free text is left out
			value = strings.ToUpper(value)
			if !countryCode.MatchString(value) {
				continue
			}
		}
		key := fold(value)
		if key == "" {
			continue
		}
		g, ok := groups[key]
		if !ok {
			g = &group{spellings: map[string]int{}}
			groups[key] = g
		}
		g.count++
		g.spellings[value]++
	}
	values := make([]audienceValue, 0, len(groups))
	for _, g := range groups {
		best, bestCount := "", 0
		for spelling, count := range g.spellings {
			if count > bestCount || (count == bestCount && spelling < best) {
				best, bestCount = spelling, count
			}
		}
		values = append(values, audienceValue{Value: best, Count: g.count})
	}
	slices.SortFunc(values, func(a, b audienceValue) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(a.Value, b.Value))
	})
	return values
}

// GET /v1/admin/notifications/audience?country=&city=&church=&assembly=: how
// many members (and phones) the filters reach, and the values each filter
// can take with the others.
func (s *Server) notificationAudience(c *gin.Context) {
	var filter audienceFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		badRequestError(c, err)
		return
	}
	filter = filter.trimmed()
	members, ids, err := s.audienceOf(c, filter)
	if err != nil {
		internalServerError(c, err)
		return
	}
	phones, err := s.store.CountDevicesOfUsers(c, ids)
	if err != nil {
		internalServerError(c, err)
		return
	}
	wanted := filter.places()
	apiResponse(c, http.StatusOK, audienceResponse{
		Recipients: len(ids),
		Phones:     phones,
		Countries:  valuesOf(members, wanted, placeCountry),
		Cities:     valuesOf(members, wanted, placeCity),
		Churches:   valuesOf(members, wanted, placeChurch),
		Assemblies: valuesOf(members, wanted, placeAssembly),
	})
}

// ----- announcements -----

// attachmentResponse is an image or a PDF of an announcement; [url] is
// relative to the API host, like the other media.
type attachmentResponse struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	Name      string `json:"name"`
	Width     int32  `json:"width"`
	Height    int32  `json:"height"`
	Color     string `json:"color"`
	SizeBytes int64  `json:"size_bytes"`
}

func toAttachmentResponse(row db.ListAnnouncementAttachmentsRow) attachmentResponse {
	kind := "image"
	if row.Medium.ContentType == "application/pdf" {
		kind = "pdf"
	}
	return attachmentResponse{
		ID:        row.Medium.ID,
		Kind:      kind,
		URL:       mediaURL(row.Medium.Key),
		Name:      row.Name,
		Width:     row.Medium.Width,
		Height:    row.Medium.Height,
		Color:     row.Medium.Color,
		SizeBytes: row.Medium.SizeBytes,
	}
}

// orEmpty: an announcement without files answers [], not null.
func orEmpty(attachments []attachmentResponse) []attachmentResponse {
	if attachments == nil {
		return []attachmentResponse{}
	}
	return attachments
}

// attachmentsOf loads the files of [ids], by announcement.
func (s *Server) attachmentsOf(c *gin.Context, ids []int64) (map[int64][]attachmentResponse, error) {
	found := map[int64][]attachmentResponse{}
	if len(ids) == 0 {
		return found, nil
	}
	rows, err := s.store.ListAnnouncementAttachments(c, ids)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		found[row.AnnouncementID] = append(found[row.AnnouncementID], toAttachmentResponse(row))
	}
	return found, nil
}

type adminNotificationResponse struct {
	ID          int64                `json:"id"`
	Title       string               `json:"title"`
	Body        string               `json:"body"`
	Audience    audienceFilter       `json:"audience"`
	Attachments []attachmentResponse `json:"attachments"`
	Author      string               `json:"author"`
	Recipients  int32                `json:"recipients"`
	// the phones the push was handed to
	Pushed    int32     `json:"pushed"`
	Read      int32     `json:"read"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) adminNotificationResponses(c *gin.Context, rows []db.ListAnnouncementsRow) ([]adminNotificationResponse, error) {
	responses := make([]adminNotificationResponse, 0, len(rows))
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.Announcement.ID
	}
	attachments, err := s.attachmentsOf(c, ids)
	if err != nil {
		return nil, err
	}
	reads := map[int64]int32{}
	if len(ids) > 0 {
		counts, err := s.store.CountAnnouncementReads(c, ids)
		if err != nil {
			return nil, err
		}
		for _, count := range counts {
			reads[count.AnnouncementID] = count.Readers
		}
	}
	for _, row := range rows {
		a := row.Announcement
		var audience audienceFilter
		_ = json.Unmarshal(a.Audience, &audience)
		responses = append(responses, adminNotificationResponse{
			ID:          a.ID,
			Title:       a.Title,
			Body:        a.Body,
			Audience:    audience,
			Attachments: orEmpty(attachments[a.ID]),
			Author:      row.Author,
			Recipients:  a.RecipientsCount,
			Pushed:      a.PushedCount,
			Read:        reads[a.ID],
			CreatedAt:   a.CreatedAt,
		})
	}
	return responses, nil
}

func (s *Server) sendAdminNotification(c *gin.Context, status int, id int64) {
	row, err := s.store.GetAnnouncement(c, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Notification")
		} else {
			internalServerError(c, err)
		}
		return
	}
	responses, err := s.adminNotificationResponses(c, []db.ListAnnouncementsRow{
		{Announcement: row.Announcement, Author: row.Author},
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, status, responses[0])
}

type notificationAttachmentRequest struct {
	MediaID int64  `json:"media_id" binding:"required,min=1"`
	Name    string `json:"name" binding:"max=200"`
}

type sendNotificationRequest struct {
	Title       string                          `json:"title"`
	Body        string                          `json:"body"`
	Audience    audienceFilter                  `json:"audience"`
	Attachments []notificationAttachmentRequest `json:"attachments" binding:"dive"`
}

func (r sendNotificationRequest) check() (title, body string, err error) {
	title, body = strings.TrimSpace(r.Title), strings.TrimSpace(r.Body)
	switch {
	case title == "":
		return "", "", errors.New("title is required")
	case utf8.RuneCountInString(title) > maxNotificationTitle:
		return "", "", fmt.Errorf("title: %d characters at most", maxNotificationTitle)
	case body == "":
		return "", "", errors.New("body is required")
	case utf8.RuneCountInString(body) > maxNotificationBody:
		return "", "", fmt.Errorf("body: %d characters at most", maxNotificationBody)
	case len(r.Attachments) > maxNotificationAttachments:
		return "", "", fmt.Errorf("%d files at most", maxNotificationAttachments)
	}
	return title, body, nil
}

// POST /v1/admin/notifications: the announcement goes to the members of the
// audience (fixed now: a member who joins later won't see it), in the app,
// and as a push to their phones.
func (s *Server) sendNotification(c *gin.Context) {
	var req sendNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	title, body, err := req.check()
	if err != nil {
		badRequestError(c, err)
		return
	}

	mediaIDs := make([]int64, len(req.Attachments))
	for i, attachment := range req.Attachments {
		mediaIDs[i] = attachment.MediaID
	}
	files, err := s.mediaByIDs(c, mediaIDs)
	if err != nil {
		internalServerError(c, err)
		return
	}
	attachments := make([]db.AnnouncementAttachmentInput, len(req.Attachments))
	for i, attachment := range req.Attachments {
		if _, ok := files[attachment.MediaID]; !ok {
			badRequestError(c, fmt.Errorf("unknown media %d", attachment.MediaID))
			return
		}
		attachments[i] = db.AnnouncementAttachmentInput{MediaID: attachment.MediaID, Name: strings.TrimSpace(attachment.Name)}
	}

	audience := req.Audience.trimmed()
	_, recipients, err := s.audienceOf(c, audience)
	if err != nil {
		internalServerError(c, err)
		return
	}
	if len(recipients) == 0 {
		c.JSON(http.StatusBadRequest, NewErrorResponse(http.StatusBadRequest, "NO_RECIPIENTS",
			errors.New("no member matches this audience")))
		return
	}

	rawAudience, _ := json.Marshal(audience)
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	announcement, err := s.store.CreateAnnouncementTx(c, db.CreateAnnouncementParams{
		Title:           title,
		Body:            body,
		Audience:        rawAudience,
		AuthorID:        nullID(authUser.ID),
		RecipientsCount: int32(len(recipients)),
	}, attachments, recipients)
	if err != nil {
		internalServerError(c, err)
		return
	}

	s.pushAnnouncement(announcement, recipients, s.firstImageURL(c, announcement.ID))
	s.sendAdminNotification(c, http.StatusCreated, announcement.ID)
}

// firstImageURL is the absolute link of the first image, shown in the push;
// empty without image, or when it is served by the API and PUBLIC_URL is not
// set (the phone couldn't reach it).
func (s *Server) firstImageURL(c *gin.Context, announcementID int64) string {
	attachments, err := s.attachmentsOf(c, []int64{announcementID})
	if err != nil {
		return ""
	}
	for _, attachment := range attachments[announcementID] {
		if attachment.Kind != "image" {
			continue
		}
		// on the CDN: already absolute
		if strings.HasPrefix(attachment.URL, "https://") || strings.HasPrefix(attachment.URL, "http://") {
			return attachment.URL
		}
		base := strings.TrimSuffix(s.config.Push.PublicURL, "/")
		if base == "" {
			return ""
		}
		return base + attachment.URL
	}
	return ""
}

// pushAnnouncement sends the push to the phones of [recipients] and counts
// the phones reached. A failure is logged: the announcement stays in the
// apps anyway.
func (s *Server) pushAnnouncement(announcement db.Announcement, recipients []int64, imageURL string) {
	// not tied to the request: a closed browser tab doesn't stop the send
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()

	tokens, err := s.store.ListDeviceTokensOfUsers(ctx, recipients)
	if err != nil {
		slog.Error("cannot read the phones of the recipients", "announcement_id", announcement.ID, "error", err)
		return
	}
	if len(tokens) == 0 {
		return
	}
	body := announcement.Body
	if utf8.RuneCountInString(body) > maxPushBody {
		body = string([]rune(body)[:maxPushBody-1]) + "…"
	}
	result, err := s.push.Send(ctx, tokens, push.Message{
		Title:    announcement.Title,
		Body:     body,
		ImageURL: imageURL,
		Data:     map[string]string{"type": "announcement", "id": strconv.FormatInt(announcement.ID, 10)},
	})
	if err != nil {
		slog.Error("push of an announcement failed", "announcement_id", announcement.ID, "error", err)
	}
	slog.Info("announcement pushed", "announcement_id", announcement.ID, "phones", len(tokens), "sent", result.Sent, "dead", len(result.Dead))
	if len(result.Dead) > 0 {
		if err := s.store.DeleteDevicesByTokens(ctx, result.Dead); err != nil {
			slog.Error("cannot forget the dead phones", "count", len(result.Dead), "error", err)
		}
	}
	if err := s.store.SetAnnouncementPushed(ctx, db.SetAnnouncementPushedParams{
		ID:          announcement.ID,
		PushedCount: int32(result.Sent),
	}); err != nil {
		slog.Error("cannot count the pushes of an announcement", "announcement_id", announcement.ID, "error", err)
	}
}

type pageRequest struct {
	Page int32 `form:"page"`
	Size int32 `form:"size"`
}

func (r pageRequest) limits() (limit, offset int32) {
	return listEventsRequest{Page: r.Page, Size: r.Size}.limits()
}

// GET /v1/admin/notifications?page=&size=: the announcements sent, latest first.
func (s *Server) adminListNotifications(c *gin.Context) {
	var req pageRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		badRequestError(c, err)
		return
	}
	limit, offset := req.limits()
	rows, err := s.store.ListAnnouncements(c, db.ListAnnouncementsParams{Lim: limit, Off: offset})
	if err != nil {
		internalServerError(c, err)
		return
	}
	total, err := s.store.CountAnnouncements(c)
	if err != nil {
		internalServerError(c, err)
		return
	}
	responses, err := s.adminNotificationResponses(c, rows)
	if err != nil {
		internalServerError(c, err)
		return
	}
	paginatedApiResponse(c, http.StatusOK, responses, MetaResponse{Total: total, Limit: limit, Offset: offset})
}

// GET /v1/admin/notifications/:id
func (s *Server) adminGetNotification(c *gin.Context) {
	var req eventIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return
	}
	s.sendAdminNotification(c, http.StatusOK, req.ID)
}

// ----- the member's notifications -----

type notificationResponse struct {
	ID          int64                `json:"id"`
	Title       string               `json:"title"`
	Body        string               `json:"body"`
	Attachments []attachmentResponse `json:"attachments"`
	CreatedAt   time.Time            `json:"created_at"`
	ReadAt      *time.Time           `json:"read_at"`
}

func (s *Server) notificationResponses(c *gin.Context, announcements []db.Announcement, readAt []sql.NullTime) ([]notificationResponse, error) {
	ids := make([]int64, len(announcements))
	for i, a := range announcements {
		ids[i] = a.ID
	}
	attachments, err := s.attachmentsOf(c, ids)
	if err != nil {
		return nil, err
	}
	responses := make([]notificationResponse, len(announcements))
	for i, a := range announcements {
		responses[i] = notificationResponse{
			ID:          a.ID,
			Title:       a.Title,
			Body:        a.Body,
			Attachments: orEmpty(attachments[a.ID]),
			CreatedAt:   a.CreatedAt,
			ReadAt:      timePtr(readAt[i]),
		}
	}
	return responses, nil
}

type myNotificationsResponse struct {
	Items  []notificationResponse `json:"items"`
	Unread int32                  `json:"unread"`
}

// GET /v1/notifications?page=&size=: the announcements the member received,
// latest first, and how many are not read yet.
func (s *Server) listMyNotifications(c *gin.Context) {
	var req pageRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		badRequestError(c, err)
		return
	}
	limit, offset := req.limits()
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	rows, err := s.store.ListMyAnnouncements(c, db.ListMyAnnouncementsParams{UserID: authUser.ID, Lim: limit, Off: offset})
	if err != nil {
		internalServerError(c, err)
		return
	}
	counts, err := s.store.CountMyAnnouncements(c, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	announcements := make([]db.Announcement, len(rows))
	readAt := make([]sql.NullTime, len(rows))
	for i, row := range rows {
		announcements[i], readAt[i] = row.Announcement, row.ReadAt
	}
	items, err := s.notificationResponses(c, announcements, readAt)
	if err != nil {
		internalServerError(c, err)
		return
	}
	paginatedApiResponse(c, http.StatusOK, myNotificationsResponse{Items: items, Unread: counts.Unread},
		MetaResponse{Total: int64(counts.Total), Limit: limit, Offset: offset})
}

func (s *Server) sendMyNotification(c *gin.Context, userID, id int64) {
	row, err := s.store.GetMyAnnouncement(c, db.GetMyAnnouncementParams{UserID: userID, ID: id})
	if err != nil {
		// another member's: as if it didn't exist
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Notification")
		} else {
			internalServerError(c, err)
		}
		return
	}
	responses, err := s.notificationResponses(c, []db.Announcement{row.Announcement}, []sql.NullTime{row.ReadAt})
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, responses[0])
}

// GET /v1/notifications/:id
func (s *Server) getMyNotification(c *gin.Context) {
	var req eventIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	s.sendMyNotification(c, authUser.ID, req.ID)
}

// PUT /v1/notifications/:id/read: opened in the app. Answers the notification.
func (s *Server) readNotification(c *gin.Context) {
	var req eventIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if err := s.store.MarkAnnouncementRead(c, db.MarkAnnouncementReadParams{ID: req.ID, UserID: authUser.ID}); err != nil {
		internalServerError(c, err)
		return
	}
	s.sendMyNotification(c, authUser.ID, req.ID)
}
