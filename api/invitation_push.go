package api

import (
	"context"
	"log"
	"strconv"
	"strings"
	"unicode/utf8"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/push"
	"github.com/gin-gonic/gin"
)

// ----- the push of an invitation to an importunity topic -----

// invitationTitles: "<name> invites you to pray", in the languages of the
// apps; the body is the topic. A phone in another language gets French.
var invitationTitles = map[string]string{
	"fr": "%s t'invite à prier",
	"en": "%s invites you to pray",
	"de": "%s lädt dich zum Gebet ein",
	"es": "%s te invita a orar",
	"ar": "%s يدعوك إلى الصلاة",
	"zh": "%s 邀请你一起祷告",
	"hi": "%s ने तुम्हें प्रार्थना के लिए बुलाया है",
}

// invitationTitle is the title for a phone in [language] ("fr", "en-US"…).
func invitationTitle(language, inviter string) string {
	base, _, _ := strings.Cut(strings.ToLower(language), "-")
	pattern, ok := invitationTitles[base]
	if !ok {
		pattern = invitationTitles["fr"]
	}
	return strings.Replace(pattern, "%s", inviter, 1)
}

// firstNameOf is how a member is called in a push: the first name, or the
// pseudo.
func (s *Server) firstNameOf(c *gin.Context, userID int64) string {
	cards, err := s.store.ListMemberCards(c, []int64{userID})
	if err != nil || len(cards) == 0 {
		return ""
	}
	if name := strings.TrimSpace(cards[0].FirstName); name != "" {
		return name
	}
	return "@" + cards[0].Username
}

// pushInvitation tells the members invited now: each phone in its language;
// a touch opens the topic ("importunity_invite"). A failure is logged: the
// invitation is in the app anyway.
func (s *Server) pushInvitation(importunity db.Importunity, inviter string, invited []int64) {
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()

	devices, err := s.store.ListDevicesOfUsers(ctx, invited)
	if err != nil || len(devices) == 0 {
		return
	}
	byLanguage := map[string][]string{}
	for _, device := range devices {
		byLanguage[device.Language] = append(byLanguage[device.Language], device.Token)
	}
	body := importunity.Title
	if utf8.RuneCountInString(body) > maxPushBody {
		body = string([]rune(body)[:maxPushBody-1]) + "…"
	}
	dead := []string{}
	for language, tokens := range byLanguage {
		result, err := s.push.Send(ctx, tokens, push.Message{
			Title: invitationTitle(language, inviter),
			Body:  body,
			Data: map[string]string{
				"type": "importunity_invite",
				"id":   strconv.FormatInt(importunity.ID, 10),
			},
		})
		if err != nil {
			log.Printf("push of the invitation to importunity %d: %v", importunity.ID, err)
		}
		dead = append(dead, result.Dead...)
	}
	if len(dead) > 0 {
		if err := s.store.DeleteDevicesByTokens(ctx, dead); err != nil {
			log.Printf("forget dead tokens: %v", err)
		}
	}
}
