package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// The days of the prayer module are written YYYY-MM-DD, like the reports.
const prayerDayLayout = reportDateLayout

var (
	prayerTopicCategories = []string{"church", "nations", "sick", "event", "fasting", "other"}
	prayerLists           = []string{"unbelievers", "believers", "backsliders", "coworkers"}
	retreatMoments        = []string{"morning", "noon", "evening", "night"}
)

// maxPrayerPeople keeps the lists to what one prays for.
const maxPrayerPeople = 500

func formatDay(t time.Time) string { return t.Format(prayerDayLayout) }

func dayPtr(t sql.NullTime) *string {
	if !t.Valid {
		return nil
	}
	day := formatDay(t.Time)
	return &day
}

func parseDay(value string) (time.Time, error) {
	day, err := time.Parse(prayerDayLayout, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q, expected YYYY-MM-DD", value)
	}
	return day, nil
}

// todayIn is the date of the community (Yaoundé) when the app sends none.
func todayIn() time.Time {
	location, err := time.LoadLocation(defaultTimeZone)
	if err != nil {
		location = time.UTC
	}
	now := time.Now().In(location)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// requestDay reads ?day= (the phone's date), today by default.
func requestDay(c *gin.Context) (time.Time, error) {
	if value := c.Query("day"); value != "" {
		return parseDay(value)
	}
	return todayIn(), nil
}

// ----- prayer topics of the community -----

// documentResponse is the PDF a topic was made from.
type documentResponse struct {
	URL       string `json:"url"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
}

type prayerTopicResponse struct {
	ID       int64             `json:"id"`
	Title    string            `json:"title"`
	Category string            `json:"category"`
	Verse    string            `json:"verse"`
	StartsOn string            `json:"starts_on"`
	EndsOn   *string           `json:"ends_on"`
	EventID  *int64            `json:"event_id"`
	Document *documentResponse `json:"document"`
	Status   string            `json:"status"`
	Points   []string          `json:"points"`
	// how many members pray for it (never who)
	People int32 `json:"people"`
	// the points the signed-in member prayed for on ?day
	PrayedToday []int32   `json:"prayed_today"`
	UpdatedAt   time.Time `json:"updated_at"`
	// when the members were told by a push; null before
	NotifiedAt *time.Time `json:"notified_at"`
}

func (s *Server) prayerTopicResponses(c *gin.Context, topics []db.PrayerTopic, userID int64, day time.Time) ([]prayerTopicResponse, error) {
	responses := make([]prayerTopicResponse, 0, len(topics))
	if len(topics) == 0 {
		return responses, nil
	}
	ids := make([]int64, len(topics))
	documentIDs := []int64{}
	for i, topic := range topics {
		ids[i] = topic.ID
		if topic.DocumentID.Valid {
			documentIDs = append(documentIDs, topic.DocumentID.Int64)
		}
	}
	points, err := s.store.ListPrayerTopicPoints(c, ids)
	if err != nil {
		return nil, err
	}
	counts, err := s.store.CountPrayerTopicPeople(c, ids)
	if err != nil {
		return nil, err
	}
	prayed, err := s.store.ListMyPrayedPoints(c, db.ListMyPrayedPointsParams{UserID: userID, Day: day, TopicIds: ids})
	if err != nil {
		return nil, err
	}
	documents := map[int64]db.Medium{}
	if len(documentIDs) > 0 {
		rows, err := s.store.ListMediaByIDs(c, documentIDs)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			documents[row.ID] = row
		}
	}

	pointsOf := map[int64][]string{}
	for _, point := range points {
		pointsOf[point.TopicID] = append(pointsOf[point.TopicID], point.Body)
	}
	people := map[int64]int32{}
	for _, count := range counts {
		people[count.TopicID] = count.People
	}
	prayedOf := map[int64][]int32{}
	for _, row := range prayed {
		prayedOf[row.TopicID] = append(prayedOf[row.TopicID], row.Position)
	}

	for _, topic := range topics {
		response := prayerTopicResponse{
			ID:          topic.ID,
			Title:       topic.Title,
			Category:    topic.Category,
			Verse:       topic.Verse,
			StartsOn:    formatDay(topic.StartsOn),
			EndsOn:      dayPtr(topic.EndsOn),
			EventID:     idPtr(topic.EventID),
			Status:      topic.Status,
			Points:      pointsOf[topic.ID],
			People:      people[topic.ID],
			PrayedToday: prayedOf[topic.ID],
			UpdatedAt:   topic.UpdatedAt,
			NotifiedAt:  timePtr(topic.NotifiedAt),
		}
		if response.Points == nil {
			response.Points = []string{}
		}
		if response.PrayedToday == nil {
			response.PrayedToday = []int32{}
		}
		slices.Sort(response.PrayedToday)
		if document, ok := documents[topic.DocumentID.Int64]; ok {
			response.Document = &documentResponse{
				URL:       mediaURL(document.Key),
				Name:      topic.DocumentName,
				SizeBytes: document.SizeBytes,
			}
		}
		responses = append(responses, response)
	}
	return responses, nil
}

func (s *Server) sendPrayerTopic(c *gin.Context, status int, topic db.PrayerTopic, day time.Time) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	responses, err := s.prayerTopicResponses(c, []db.PrayerTopic{topic}, authUser.ID, day)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, status, responses[0])
}

// GET /v1/prayer/topics?day=&archive=true&page=&size=: the topics of the
// day (all of them), or the ended ones, latest first.
func (s *Server) listPrayerTopics(c *gin.Context) {
	day, err := requestDay(c)
	if err != nil {
		badRequestError(c, err)
		return
	}
	var topics []db.PrayerTopic
	if c.Query("archive") == "true" {
		var page listEventsRequest
		if err := c.ShouldBindQuery(&page); err != nil {
			badRequestError(c, err)
			return
		}
		limit, offset := page.limits()
		topics, err = s.store.ListPastPrayerTopics(c, db.ListPastPrayerTopicsParams{Today: day, Lim: limit, Off: offset})
	} else {
		topics, err = s.store.ListCurrentPrayerTopics(c, day)
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	responses, err := s.prayerTopicResponses(c, topics, authUser.ID, day)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, responses)
}

// loadPrayerTopic finds the topic of the URL. A draft is only for the admins.
func (s *Server) loadPrayerTopic(c *gin.Context, allowDraft bool) (db.PrayerTopic, bool) {
	var req eventIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return db.PrayerTopic{}, false
	}
	topic, err := s.store.GetPrayerTopic(c, req.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Prayer topic")
		} else {
			internalServerError(c, err)
		}
		return db.PrayerTopic{}, false
	}
	if topic.Status != "published" && !allowDraft {
		resourceNotFoundError(c, "Prayer topic")
		return db.PrayerTopic{}, false
	}
	return topic, true
}

// GET /v1/prayer/topics/:id?day=
func (s *Server) getPrayerTopic(c *gin.Context) {
	topic, ok := s.loadPrayerTopic(c, false)
	if !ok {
		return
	}
	day, err := requestDay(c)
	if err != nil {
		badRequestError(c, err)
		return
	}
	s.sendPrayerTopic(c, http.StatusOK, topic, day)
}

type prayedRequest struct {
	Day       string  `json:"day" binding:"required"`
	Positions []int32 `json:"positions"`
}

// PUT /v1/prayer/topics/:id/prayed: the points the member prayed for on a
// day (all of them: unchecking a point removes it). Answers the topic.
func (s *Server) setPrayedPoints(c *gin.Context) {
	topic, ok := s.loadPrayerTopic(c, false)
	if !ok {
		return
	}
	var req prayedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	day, err := parseDay(req.Day)
	if err != nil {
		badRequestError(c, err)
		return
	}
	points, err := s.store.ListPrayerTopicPoints(c, []int64{topic.ID})
	if err != nil {
		internalServerError(c, err)
		return
	}
	positions := []int32{}
	for _, position := range req.Positions {
		if position < 1 || int(position) > len(points) {
			badRequestError(c, fmt.Errorf("positions: 1 to %d", len(points)))
			return
		}
		if !slices.Contains(positions, position) {
			positions = append(positions, position)
		}
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if err := s.store.SetMyPrayedPointsTx(c, topic.ID, authUser.ID, day, positions); err != nil {
		internalServerError(c, err)
		return
	}
	s.sendPrayerTopic(c, http.StatusOK, topic, day)
}

// ----- admins -----

// GET /v1/admin/prayer/topics?page=&size=: every topic, drafts included.
func (s *Server) adminListPrayerTopics(c *gin.Context) {
	var page listEventsRequest
	if err := c.ShouldBindQuery(&page); err != nil {
		badRequestError(c, err)
		return
	}
	limit, offset := page.limits()
	topics, err := s.store.ListAllPrayerTopics(c, db.ListAllPrayerTopicsParams{Lim: limit, Off: offset})
	if err != nil {
		internalServerError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	responses, err := s.prayerTopicResponses(c, topics, authUser.ID, todayIn())
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, responses)
}

type prayerTopicRequest struct {
	Title        string  `json:"title" binding:"required"`
	Category     string  `json:"category"`
	Verse        string  `json:"verse"`
	StartsOn     string  `json:"starts_on" binding:"required"`
	EndsOn       *string `json:"ends_on"`
	EventID      *int64  `json:"event_id"`
	DocumentID   *int64  `json:"document_id"`
	DocumentName string  `json:"document_name"`
	// draft (default) or published
	Status string   `json:"status"`
	Points []string `json:"points"`
	// « Prévenir les membres » the first time it is published (true when
	// absent)
	Notify *bool `json:"notify"`
}

// prayerTopicParams checks the request and turns it into the columns of a topic.
func (s *Server) prayerTopicParams(c *gin.Context, req prayerTopicRequest) (db.CreatePrayerTopicParams, []string, error) {
	params := db.CreatePrayerTopicParams{
		Title:        strings.TrimSpace(req.Title),
		Category:     req.Category,
		Verse:        strings.TrimSpace(req.Verse),
		DocumentName: strings.TrimSpace(req.DocumentName),
		Status:       req.Status,
	}
	if params.Title == "" {
		return params, nil, fmt.Errorf("title is required")
	}
	if params.Category == "" {
		params.Category = "church"
	}
	if !slices.Contains(prayerTopicCategories, params.Category) {
		return params, nil, fmt.Errorf("category must be one of %s", strings.Join(prayerTopicCategories, ", "))
	}
	if params.Status == "" {
		params.Status = "draft"
	}
	if params.Status != "draft" && params.Status != "published" {
		return params, nil, fmt.Errorf("status must be draft or published")
	}
	startsOn, err := parseDay(req.StartsOn)
	if err != nil {
		return params, nil, err
	}
	params.StartsOn = startsOn
	if req.EndsOn != nil && *req.EndsOn != "" {
		endsOn, err := parseDay(*req.EndsOn)
		if err != nil {
			return params, nil, err
		}
		if endsOn.Before(startsOn) {
			return params, nil, fmt.Errorf("ends_on is before starts_on")
		}
		params.EndsOn = sql.NullTime{Time: endsOn, Valid: true}
	}
	if req.EventID != nil && *req.EventID != 0 {
		if _, err := s.store.GetEvent(c, *req.EventID); err != nil {
			return params, nil, fmt.Errorf("unknown event_id %d", *req.EventID)
		}
		params.EventID = nullID(*req.EventID)
	}
	if req.DocumentID != nil && *req.DocumentID != 0 {
		rows, err := s.store.ListMediaByIDs(c, []int64{*req.DocumentID})
		if err != nil {
			return params, nil, err
		}
		if len(rows) != 1 || rows[0].ContentType != "application/pdf" {
			return params, nil, fmt.Errorf("document_id %d is not a PDF", *req.DocumentID)
		}
		params.DocumentID = nullID(*req.DocumentID)
	}
	points := []string{}
	for i, point := range req.Points {
		point = strings.TrimSpace(point)
		if point == "" {
			continue
		}
		if len([]rune(point)) > 1000 {
			return params, nil, fmt.Errorf("points[%d]: 1000 characters at most", i)
		}
		points = append(points, point)
	}
	// the points are optional: the app shows the title and the PDF
	if len(points) > 100 {
		return params, nil, fmt.Errorf("100 points at most")
	}
	return params, points, nil
}

// POST /v1/admin/prayer/topics
func (s *Server) createPrayerTopic(c *gin.Context) {
	var req prayerTopicRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	params, points, err := s.prayerTopicParams(c, req)
	if err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	params.CreatedBy = nullID(authUser.ID)
	topic, err := s.store.SavePrayerTopicTx(c, 0, params, points)
	if err != nil {
		internalServerError(c, err)
		return
	}
	s.sendPrayerTopic(c, http.StatusCreated, s.announcedPrayerTopic(c, topic, req.Notify), todayIn())
}

// PUT /v1/admin/prayer/topics/:id: the whole topic, its points included.
func (s *Server) updatePrayerTopic(c *gin.Context) {
	topic, ok := s.loadPrayerTopic(c, true)
	if !ok {
		return
	}
	var req prayerTopicRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	params, points, err := s.prayerTopicParams(c, req)
	if err != nil {
		badRequestError(c, err)
		return
	}
	updated, err := s.store.SavePrayerTopicTx(c, topic.ID, params, points)
	if err != nil {
		internalServerError(c, err)
		return
	}
	s.sendPrayerTopic(c, http.StatusOK, s.announcedPrayerTopic(c, updated, req.Notify), todayIn())
}

// DELETE /v1/admin/prayer/topics/:id (the PDF stays in the media storage).
func (s *Server) deletePrayerTopic(c *gin.Context) {
	topic, ok := s.loadPrayerTopic(c, true)
	if !ok {
		return
	}
	if _, err := s.store.DeletePrayerTopic(c, topic.ID); err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusNoContent, nil)
}

// ----- the members' own lists -----

type prayerPersonResponse struct {
	ID           int64     `json:"id"`
	List         string    `json:"list"`
	Name         string    `json:"name"`
	Note         string    `json:"note"`
	LastPrayedOn *string   `json:"last_prayed_on"`
	AnsweredOn   *string   `json:"answered_on"`
	CreatedAt    time.Time `json:"created_at"`
}

func toPrayerPerson(person db.PrayerPerson) prayerPersonResponse {
	return prayerPersonResponse{
		ID:           person.ID,
		List:         person.List,
		Name:         person.Name,
		Note:         person.Note,
		LastPrayedOn: dayPtr(person.LastPrayedOn),
		AnsweredOn:   dayPtr(person.AnsweredOn),
		CreatedAt:    person.CreatedAt,
	}
}

// GET /v1/prayer/people: all the people of the signed-in member's lists.
func (s *Server) listPrayerPeople(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	people, err := s.store.ListPrayerPeople(c, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	responses := make([]prayerPersonResponse, 0, len(people))
	for _, person := range people {
		responses = append(responses, toPrayerPerson(person))
	}
	apiResponse(c, http.StatusOK, responses)
}

type prayerPersonRequest struct {
	List string `json:"list" binding:"required"`
	Name string `json:"name" binding:"required"`
	Note string `json:"note"`
	// believed, came back, prayer answered; null or "" to clear
	AnsweredOn *string `json:"answered_on"`
}

func (r prayerPersonRequest) check() (name, note string, answeredOn sql.NullTime, err error) {
	if !slices.Contains(prayerLists, r.List) {
		return "", "", answeredOn, fmt.Errorf("list must be one of %s", strings.Join(prayerLists, ", "))
	}
	name = strings.TrimSpace(r.Name)
	note = strings.TrimSpace(r.Note)
	if name == "" {
		return "", "", answeredOn, fmt.Errorf("name is required")
	}
	if len([]rune(name)) > 100 || len([]rune(note)) > 300 {
		return "", "", answeredOn, fmt.Errorf("name: 100 characters at most, note: 300")
	}
	if r.AnsweredOn != nil && *r.AnsweredOn != "" {
		day, err := parseDay(*r.AnsweredOn)
		if err != nil {
			return "", "", answeredOn, err
		}
		answeredOn = sql.NullTime{Time: day, Valid: true}
	}
	return name, note, answeredOn, nil
}

// POST /v1/prayer/people
func (s *Server) createPrayerPerson(c *gin.Context) {
	var req prayerPersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	name, note, _, err := req.check()
	if err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	count, err := s.store.CountPrayerPeople(c, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	if count >= maxPrayerPeople {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "TOO_MANY_PEOPLE",
			fmt.Errorf("%d people at most", maxPrayerPeople)))
		return
	}
	person, err := s.store.CreatePrayerPerson(c, db.CreatePrayerPersonParams{
		UserID: authUser.ID,
		List:   req.List,
		Name:   name,
		Note:   note,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusCreated, toPrayerPerson(person))
}

// PUT /v1/prayer/people/:id: someone of the member's lists, and only theirs.
func (s *Server) updatePrayerPerson(c *gin.Context) {
	var uri eventIDRequest
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req prayerPersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	name, note, answeredOn, err := req.check()
	if err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	person, err := s.store.UpdatePrayerPerson(c, db.UpdatePrayerPersonParams{
		ID:         uri.ID,
		UserID:     authUser.ID,
		List:       req.List,
		Name:       name,
		Note:       note,
		AnsweredOn: answeredOn,
	})
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "Person")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, toPrayerPerson(person))
}

// DELETE /v1/prayer/people/:id
func (s *Server) deletePrayerPerson(c *gin.Context) {
	var uri eventIDRequest
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	deleted, err := s.store.DeletePrayerPerson(c, db.DeletePrayerPersonParams{ID: uri.ID, UserID: authUser.ID})
	if err != nil {
		internalServerError(c, err)
		return
	}
	if deleted == 0 {
		resourceNotFoundError(c, "Person")
		return
	}
	apiResponse(c, http.StatusNoContent, nil)
}

type prayedPeopleRequest struct {
	Day string  `json:"day" binding:"required"`
	IDs []int64 `json:"ids"`
}

// POST /v1/prayer/people/prayed: a retreat prayed for them on [day].
func (s *Server) markPrayerPeoplePrayed(c *gin.Context) {
	var req prayedPeopleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	day, err := parseDay(req.Day)
	if err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if len(req.IDs) > 0 {
		if _, err := s.store.MarkPrayerPeoplePrayed(c, db.MarkPrayerPeoplePrayedParams{
			Day: day, UserID: authUser.ID, Ids: req.IDs,
		}); err != nil {
			internalServerError(c, err)
			return
		}
	}
	s.listPrayerPeople(c)
}

// ----- the 15-minute retreats -----

type prayerRetreatResponse struct {
	Day     string    `json:"day"`
	Moment  string    `json:"moment"`
	Minutes int32     `json:"minutes"`
	DoneAt  time.Time `json:"done_at"`
}

func toPrayerRetreat(retreat db.PrayerRetreat) prayerRetreatResponse {
	return prayerRetreatResponse{
		Day:     formatDay(retreat.Day),
		Moment:  retreat.Moment,
		Minutes: retreat.Minutes,
		DoneAt:  retreat.DoneAt,
	}
}

// GET /v1/prayer/retreats?from=&to=: the last 30 days by default, a year at most.
func (s *Server) listPrayerRetreats(c *gin.Context) {
	from, to, err := parseReportRange(c)
	if err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	retreats, err := s.store.ListPrayerRetreats(c, db.ListPrayerRetreatsParams{UserID: authUser.ID, FromDay: from, ToDay: to})
	if err != nil {
		internalServerError(c, err)
		return
	}
	responses := make([]prayerRetreatResponse, 0, len(retreats))
	for _, retreat := range retreats {
		responses = append(responses, toPrayerRetreat(retreat))
	}
	apiResponse(c, http.StatusOK, responses)
}

type retreatURI struct {
	Day    string `uri:"day" binding:"required"`
	Moment string `uri:"moment" binding:"required"`
}

func readRetreatURI(c *gin.Context) (time.Time, string, bool) {
	var uri retreatURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return time.Time{}, "", false
	}
	day, err := parseDay(uri.Day)
	if err != nil {
		badRequestError(c, err)
		return time.Time{}, "", false
	}
	if !slices.Contains(retreatMoments, uri.Moment) {
		badRequestError(c, fmt.Errorf("moment must be one of %s", strings.Join(retreatMoments, ", ")))
		return time.Time{}, "", false
	}
	return day, uri.Moment, true
}

type retreatRequest struct {
	Minutes int32 `json:"minutes"`
}

// PUT /v1/prayer/retreats/:day/:moment: the retreat is made (15 minutes by default).
func (s *Server) savePrayerRetreat(c *gin.Context) {
	day, moment, ok := readRetreatURI(c)
	if !ok {
		return
	}
	var req retreatRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequestError(c, err)
			return
		}
	}
	if req.Minutes == 0 {
		req.Minutes = 15
	}
	if req.Minutes < 1 || req.Minutes > 240 {
		badRequestError(c, fmt.Errorf("minutes: 1 to 240"))
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	retreat, err := s.store.UpsertPrayerRetreat(c, db.UpsertPrayerRetreatParams{
		UserID: authUser.ID, Day: day, Moment: moment, Minutes: req.Minutes,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, toPrayerRetreat(retreat))
}

// DELETE /v1/prayer/retreats/:day/:moment: marked by mistake.
func (s *Server) deletePrayerRetreat(c *gin.Context) {
	day, moment, ok := readRetreatURI(c)
	if !ok {
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if _, err := s.store.DeletePrayerRetreat(c, db.DeletePrayerRetreatParams{UserID: authUser.ID, Day: day, Moment: moment}); err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusNoContent, nil)
}
