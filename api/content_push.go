package api

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/push"
	"github.com/gin-gonic/gin"
)

// The new contents of the community are announced to every member by a
// push, once (notified_at, migration 000028): the first time a teaching, a
// document, an event or a prayer topic is published with « Prévenir les
// membres » (notify, true when absent). The title says what it is, in the
// language of each phone; the body names it; a touch opens it in the app.

var newTeachingTitles = map[string]string{
	"fr": "Nouvel enseignement",
	"en": "New teaching",
	"de": "Neue Lehre",
	"es": "Nueva enseñanza",
	"ar": "تعليم جديد",
	"zh": "新的教导",
	"hi": "नई शिक्षा",
}

var newDocumentTitles = map[string]string{
	"fr": "Nouveau document",
	"en": "New document",
	"de": "Neues Dokument",
	"es": "Nuevo documento",
	"ar": "مستند جديد",
	"zh": "新文档",
	"hi": "नया दस्तावेज़",
}

var newEventTitles = map[string]string{
	"fr": "Nouvel évènement",
	"en": "New event",
	"de": "Neue Veranstaltung",
	"es": "Nuevo evento",
	"ar": "حدث جديد",
	"zh": "新活动",
	"hi": "नया कार्यक्रम",
}

var newPrayerTopicTitles = map[string]string{
	"fr": "Nouveau sujet de prière",
	"en": "New prayer topic",
	"de": "Neues Gebetsanliegen",
	"es": "Nuevo motivo de oración",
	"ar": "موضوع صلاة جديد",
	"zh": "新的祷告事项",
	"hi": "प्रार्थना का नया विषय",
}

// notifies: « Prévenir les membres », checked unless said otherwise.
func notifies(notify *bool) bool {
	return notify == nil || *notify
}

// announceTeaching: « Nouvel enseignement », the title and the speaker, with
// the cover.
func (s *Server) announceTeaching(c *gin.Context, id int64, notify *bool) {
	if !notifies(notify) {
		return
	}
	row, err := s.store.ClaimTeachingNotification(c, id)
	if !s.claimed(err, "teaching", id) {
		return
	}
	body := row.Title
	if row.Speaker != "" {
		body += " · " + row.Speaker
	}
	s.pushToEveryone(newTeachingTitles, body, s.pushImageOf(c, row.CoverID), "teaching", id)
}

// announceDocument: « Nouveau document » and its title.
func (s *Server) announceDocument(c *gin.Context, id int64, notify *bool) {
	if !notifies(notify) {
		return
	}
	title, err := s.store.ClaimDocumentNotification(c, id)
	if !s.claimed(err, "document", id) {
		return
	}
	s.pushToEveryone(newDocumentTitles, title, "", "document", id)
}

// announceEvent: « Nouvel évènement », the title, the day and the city, with
// the poster.
func (s *Server) announceEvent(c *gin.Context, id int64, notify *bool) {
	if !notifies(notify) {
		return
	}
	row, err := s.store.ClaimEventNotification(c, id)
	if !s.claimed(err, "event", id) {
		return
	}
	startsAt := row.StartsAt
	if location, err := time.LoadLocation(row.TimeZone); err == nil {
		startsAt = startsAt.In(location)
	}
	body := row.Title + " · " + startsAt.Format("02/01")
	if row.City != "" {
		body += " · " + row.City
	}
	s.pushToEveryone(newEventTitles, body, s.pushImageOf(c, row.PosterID), "event", id)
}

// announcePrayerTopic: « Nouveau sujet de prière » and its title.
func (s *Server) announcePrayerTopic(c *gin.Context, id int64, notify *bool) {
	if !notifies(notify) {
		return
	}
	title, err := s.store.ClaimPrayerTopicNotification(c, id)
	if !s.claimed(err, "prayer_topic", id) {
		return
	}
	s.pushToEveryone(newPrayerTopicTitles, title, "", "prayer_topic", id)
}

// announcedEvent announces [event] when it is to be, and gives it back as
// saved (with its notified_at).
func (s *Server) announcedEvent(c *gin.Context, event db.Event, notify *bool) db.Event {
	if event.NotifiedAt.Valid || !notifies(notify) {
		return event
	}
	s.announceEvent(c, event.ID, notify)
	if saved, err := s.store.GetEvent(c, event.ID); err == nil {
		return saved
	}
	return event
}

// announcedPrayerTopic announces [topic] when it is to be, and gives it back
// as saved (with its notified_at).
func (s *Server) announcedPrayerTopic(c *gin.Context, topic db.PrayerTopic, notify *bool) db.PrayerTopic {
	if topic.NotifiedAt.Valid || !notifies(notify) {
		return topic
	}
	s.announcePrayerTopic(c, topic.ID, notify)
	if saved, err := s.store.GetPrayerTopic(c, topic.ID); err == nil {
		return saved
	}
	return topic
}

// claimed: the content is to be announced now. Not when it is a draft or
// was announced already (no row); an error is logged, the content is saved.
func (s *Server) claimed(err error, kind string, id int64) bool {
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		slog.Error("cannot mark a content as announced", "type", kind, "id", id, "error", err)
		return false
	}
	return true
}

// pushImageOf is the absolute link of an image for the push; empty without
// one, or when the API serves it and PUBLIC_URL is not set (the phone
// couldn't reach it).
func (s *Server) pushImageOf(c *gin.Context, id sql.NullInt64) string {
	if !id.Valid {
		return ""
	}
	found, err := s.mediaByIDs(c, []int64{id.Int64})
	if err != nil {
		return ""
	}
	url := found[id.Int64].URL
	if url == "" || strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
		return url
	}
	base := strings.TrimSuffix(s.config.Push.PublicURL, "/")
	if base == "" {
		return ""
	}
	return base + url
}

// pushToEveryone sends to every phone, each in its language. A failure is
// logged: the content is in the app anyway.
func (s *Server) pushToEveryone(titles map[string]string, body, imageURL, kind string, id int64) {
	// not tied to the request: a closed browser tab doesn't stop the send
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()

	devices, err := s.store.ListAllDevices(ctx)
	if err != nil {
		slog.Error("cannot read the phones for a content push", "type", kind, "id", id, "error", err)
		return
	}
	if utf8.RuneCountInString(body) > maxPushBody {
		body = string([]rune(body)[:maxPushBody-1]) + "…"
	}
	byLanguage := map[string][]string{}
	for _, device := range devices {
		byLanguage[device.Language] = append(byLanguage[device.Language], device.Token)
	}
	data := map[string]string{"type": kind, "id": strconv.FormatInt(id, 10)}
	sent := 0
	dead := []string{}
	for language, tokens := range byLanguage {
		result, err := s.push.Send(ctx, tokens, push.Message{
			Title:    titleIn(titles, language, ""),
			Body:     body,
			ImageURL: imageURL,
			Data:     data,
		})
		if err != nil {
			slog.Error("content push failed", "type", kind, "id", id, "language", language, "error", err)
		}
		sent += result.Sent
		dead = append(dead, result.Dead...)
	}
	slog.Info("content pushed", "type", kind, "id", id, "phones", len(devices), "sent", sent, "dead", len(dead))
	if len(dead) > 0 {
		if err := s.store.DeleteDevicesByTokens(ctx, dead); err != nil {
			slog.Error("cannot forget the dead phones", "count", len(dead), "error", err)
		}
	}
}
