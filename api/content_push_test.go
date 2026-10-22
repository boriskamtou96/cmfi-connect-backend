package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/push"
	"github.com/stretchr/testify/require"
)

// pushedTo: the pushes that reached the phone [token] (every phone of the
// test database gets the contents, a language at a time).
func pushedTo(sender *fakeSender, token string) []push.Message {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	found := []push.Message{}
	for i, tokens := range sender.tokens {
		if slices.Contains(tokens, token) {
			found = append(found, sender.messages[i])
		}
	}
	return found
}

// aPhone: a member with a phone in [language].
func aPhone(t *testing.T, router http.Handler, language string) string {
	t.Helper()
	m := registerMember(t, router, nil)
	token := "phone-" + uniqueName()
	response := send(t, router, http.MethodPut, m.Token, "/v1/devices",
		map[string]string{"token": token, "platform": "android", "language": language})
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	return token
}

func TestANewTeachingIsAnnouncedOnceWhenPublished(t *testing.T) {
	router, sender := newPushRouter(t, "")
	admin := staffAdmin(t, router)
	french := aPhone(t, router, "fr")
	english := aPhone(t, router, "en-GB")

	uploaded := uploadAudio(t, router, admin.Token, "La croix.mp3", mp3Bytes)
	require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
	audio := decode[teachingAudioResponse](t, uploaded)
	body := map[string]any{
		"title": "La croix", "speaker": "Zacharias Fomum",
		"audio_id": audio.ID, "duration_seconds": 60, "published": false,
	}

	// a draft: nothing
	draft := createTeachingFor(t, router, admin.Token, body)
	require.Nil(t, draft.NotifiedAt)
	require.Empty(t, pushedTo(sender, french))

	// published: « Nouvel enseignement », each phone in its language
	body["published"] = true
	path := fmt.Sprintf("/v1/admin/teachings/%d", draft.ID)
	published := send(t, router, http.MethodPut, admin.Token, path, body)
	require.Equal(t, http.StatusOK, published.Code, published.Body.String())
	require.NotNil(t, decode[teachingResponse](t, published).NotifiedAt)
	messages := pushedTo(sender, french)
	require.Len(t, messages, 1)
	require.Equal(t, "Nouvel enseignement", messages[0].Title)
	require.Equal(t, "La croix · Zacharias Fomum", messages[0].Body)
	require.Equal(t, map[string]string{"type": "teaching", "id": fmt.Sprint(draft.ID)}, messages[0].Data)
	require.Equal(t, "New teaching", pushedTo(sender, english)[0].Title)

	// a fix afterwards: nothing more
	body["title"] = "La croix du Christ"
	fixed := send(t, router, http.MethodPut, admin.Token, path, body)
	require.Equal(t, http.StatusOK, fixed.Code, fixed.Body.String())
	require.Len(t, pushedTo(sender, french), 1)
}

func TestADocumentPublishedWithoutTellingCanBeAnnouncedLater(t *testing.T) {
	router, sender := newPushRouter(t, "")
	admin := staffAdmin(t, router)
	phone := aPhone(t, router, "fr")

	uploaded := uploadDocument(t, router, admin.Token, "Règlement intérieur.pdf", pdfBytes)
	require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
	file := decode[libraryUploadResponse](t, uploaded)
	body := map[string]any{"title": "Règlement intérieur", "file_id": file.ID, "published": true, "notify": false}

	// « Prévenir les membres » unchecked: published silently
	document := createLibraryDocument(t, router, admin.Token, body)
	require.Nil(t, document.NotifiedAt)
	require.Empty(t, pushedTo(sender, phone))

	// checked later: announced then, once
	body["notify"] = true
	path := fmt.Sprintf("/v1/admin/documents/%d", document.ID)
	for range 2 {
		response := send(t, router, http.MethodPut, admin.Token, path, body)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.NotNil(t, decode[libraryDocumentResponse](t, response).NotifiedAt)
	}
	messages := pushedTo(sender, phone)
	require.Len(t, messages, 1)
	require.Equal(t, "Nouveau document", messages[0].Title)
	require.Equal(t, "Règlement intérieur", messages[0].Body)
	require.Equal(t, "document", messages[0].Data["type"])
}

func TestANewEventAndANewPrayerTopicAreAnnounced(t *testing.T) {
	router, sender := newPushRouter(t, "")
	admin := staffAdmin(t, router)
	phone := aPhone(t, router, "fr")

	// published at once: « Nouvel évènement », the title, the day, the city
	event := createEvent(t, router, admin.Token, nil)
	require.NotNil(t, event.NotifiedAt)
	messages := pushedTo(sender, phone)
	require.Len(t, messages, 1)
	require.Equal(t, "Nouvel évènement", messages[0].Title)
	require.True(t, strings.HasPrefix(messages[0].Body, event.Title+" · "), messages[0].Body)
	require.True(t, strings.HasSuffix(messages[0].Body, " · Yaoundé"), messages[0].Body)
	require.Equal(t, map[string]string{"type": "event", "id": fmt.Sprint(event.ID)}, messages[0].Data)

	// a cancelled one is not announced
	cancelled := createEvent(t, router, admin.Token, map[string]any{"status": "cancelled"})
	require.Nil(t, cancelled.NotifiedAt)
	require.Len(t, pushedTo(sender, phone), 1)

	topic := createPrayerTopic(t, router, admin.Token, nil)
	require.NotNil(t, topic.NotifiedAt)
	messages = pushedTo(sender, phone)
	require.Len(t, messages, 2)
	require.Equal(t, "Nouveau sujet de prière", messages[1].Title)
	require.Equal(t, topic.Title, messages[1].Body)
	require.Equal(t, map[string]string{"type": "prayer_topic", "id": fmt.Sprint(topic.ID)}, messages[1].Data)

	// saved again: nothing more
	again := send(t, router, http.MethodPut, admin.Token, fmt.Sprintf("/v1/admin/prayer/topics/%d", topic.ID), map[string]any{
		"title": topic.Title, "category": "event", "starts_on": day(-1), "status": "published",
	})
	require.Equal(t, http.StatusOK, again.Code, again.Body.String())
	require.Len(t, pushedTo(sender, phone), 2)
}
