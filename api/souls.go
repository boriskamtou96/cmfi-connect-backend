package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// Suivi des âmes: the people a member evangelised and follows, synchronised
// from their phones (which keep a copy to work offline). Only that member
// reads them: names and phones of other people, never logged.

var soulStages = []string{"met", "interested", "received", "followUp", "church", "baptized"}

const (
	maxSoulID    = 64
	maxSoulText  = 200
	maxSoulNotes = 200
	maxSoulNote  = 2000
)

type soulNote struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// soulBody: a soul as the app writes it (the same JSON both ways).
type soulBody struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Phone       string     `json:"phone"`
	Place       string     `json:"place"`
	MetOn       string     `json:"met_on"`
	Stage       string     `json:"stage"`
	NextContact *string    `json:"next_contact,omitempty"`
	Notes       []soulNote `json:"notes"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toSoulBody(soul db.Soul) soulBody {
	body := soulBody{
		ID: soul.ID, Name: soul.Name, Phone: soul.Phone, Place: soul.Place,
		MetOn: soul.MetOn.Format(time.DateOnly), Stage: soul.Stage, Notes: []soulNote{},
		UpdatedAt: soul.UpdatedAt,
	}
	if soul.NextContact.Valid {
		day := soul.NextContact.Time.Format(time.DateOnly)
		body.NextContact = &day
	}
	_ = json.Unmarshal(soul.Notes, &body.Notes)
	return body
}

// GET /v1/souls: all the member's souls, the latest added first.
func (s *Server) listSouls(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	souls, err := s.store.ListSouls(c, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	bodies := make([]soulBody, len(souls))
	for i, soul := range souls {
		bodies[i] = toSoulBody(soul)
	}
	apiResponse(c, http.StatusOK, bodies)
}

// params checks a soul sent by the app.
func (body soulBody) params(userID int64, id string) (db.UpsertSoulParams, error) {
	params := db.UpsertSoulParams{
		UserID: userID, ID: id,
		Name:  strings.TrimSpace(body.Name),
		Phone: strings.TrimSpace(body.Phone),
		Place: strings.TrimSpace(body.Place),
		Stage: body.Stage,
	}
	switch {
	case id == "" || utf8.RuneCountInString(id) > maxSoulID:
		return params, fmt.Errorf("id: 1 to %d characters", maxSoulID)
	case params.Name == "" || utf8.RuneCountInString(params.Name) > maxSoulText:
		return params, fmt.Errorf("name: 1 to %d characters", maxSoulText)
	case utf8.RuneCountInString(params.Phone) > maxSoulText || utf8.RuneCountInString(params.Place) > maxSoulText:
		return params, fmt.Errorf("phone and place: %d characters at most", maxSoulText)
	case len(body.Notes) > maxSoulNotes:
		return params, fmt.Errorf("%d notes at most", maxSoulNotes)
	}
	if params.Stage == "" {
		params.Stage = "met"
	}
	if !slices.Contains(soulStages, params.Stage) {
		return params, fmt.Errorf("unknown stage %q", params.Stage)
	}
	metOn, err := time.Parse(time.DateOnly, body.MetOn)
	if err != nil {
		return params, fmt.Errorf("met_on: YYYY-MM-DD")
	}
	params.MetOn = metOn
	if body.NextContact != nil && *body.NextContact != "" {
		next, err := time.Parse(time.DateOnly, *body.NextContact)
		if err != nil {
			return params, fmt.Errorf("next_contact: YYYY-MM-DD")
		}
		params.NextContact = sql.NullTime{Time: next, Valid: true}
	}
	notes := []soulNote{}
	for _, note := range body.Notes {
		text := strings.TrimSpace(note.Text)
		if text == "" {
			continue
		}
		if utf8.RuneCountInString(text) > maxSoulNote {
			return params, fmt.Errorf("a note: %d characters at most", maxSoulNote)
		}
		notes = append(notes, soulNote{At: note.At, Text: text})
	}
	params.Notes, _ = json.Marshal(notes)
	return params, nil
}

// PUT /v1/souls/:id: the soul added or changed on a phone.
func (s *Server) saveSoul(c *gin.Context) {
	var body soulBody
	if err := c.ShouldBindJSON(&body); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	params, err := body.params(authUser.ID, c.Param("id"))
	if err != nil {
		badRequestError(c, err)
		return
	}
	soul, err := s.store.UpsertSoul(c, params)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, toSoulBody(soul))
}

// DELETE /v1/souls/:id: gone (204 also when it was not there: a phone may
// send it again).
func (s *Server) deleteSoul(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if err := s.store.DeleteSoul(c, db.DeleteSoulParams{UserID: authUser.ID, ID: c.Param("id")}); err != nil {
		internalServerError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
