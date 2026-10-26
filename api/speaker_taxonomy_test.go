package api

import (
	"fmt"
	"image/color"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// createSpeakerFor makes a fiche through the admin API.
func createSpeakerFor(t *testing.T, router http.Handler, token string, body map[string]any) adminTeachingSpeakerResponse {
	created := send(t, router, http.MethodPost, token, "/v1/admin/speakers", body)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	return decode[adminTeachingSpeakerResponse](t, created)
}

func listedSpeakersFor(t *testing.T, router http.Handler, token string) []adminTeachingSpeakerResponse {
	response := send(t, router, http.MethodGet, token, "/v1/admin/speakers", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[[]adminTeachingSpeakerResponse](t, response)
}

// speakerNamed finds one fiche of the admin list.
func speakerNamed(t *testing.T, speakers []adminTeachingSpeakerResponse, id int64) adminTeachingSpeakerResponse {
	for _, speaker := range speakers {
		if speaker.ID == id {
			return speaker
		}
	}
	require.FailNow(t, "speaker not in the list", "id %d", id)
	return adminTeachingSpeakerResponse{}
}

func TestAdminsManageTeachingSpeakers(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)
	word := uniqueName()

	// only an admin writes the fiches
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodPost, boris.Token, "/v1/admin/speakers", map[string]any{
		"name": "Un orateur " + word,
	}).Code)

	photo := decode[mediaResponse](t, upload(t, router, admin.Token, pngBytes(t, 10, 10, color.White)))
	name := "Théodore Andoseh " + word
	speaker := createSpeakerFor(t, router, admin.Token, map[string]any{
		"name": "  " + name + "  ", "role": " Pasteur ", "bio": " Il prêche la sainteté. ", "photo_id": photo.ID,
	})
	// the spaces are tidied, the name and its slug saved
	require.Equal(t, name, speaker.Name)
	require.Contains(t, speaker.Slug, "theodore-andoseh")
	require.Equal(t, "Pasteur", speaker.Role)
	require.Equal(t, "Il prêche la sainteté.", speaker.Bio)
	require.NotNil(t, speaker.Photo)
	require.Equal(t, photo.ID, speaker.Photo.ID)
	require.False(t, speaker.Archived)
	require.Zero(t, speaker.TeachingCount)

	// the same name is refused, capitals and accents left aside
	duplicate := send(t, router, http.MethodPost, admin.Token, "/v1/admin/speakers", map[string]any{
		"name": strings.ToUpper("theodore andoseh " + word),
	})
	require.Equal(t, http.StatusBadRequest, duplicate.Code, duplicate.Body.String())
	// so is a name left empty, and a photo that is not an image
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPost, admin.Token, "/v1/admin/speakers", map[string]any{
		"name": "   ",
	}).Code)
	sound := decode[teachingAudioResponse](t, uploadAudio(t, router, admin.Token, "x.mp3", mp3Bytes))
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPost, admin.Token, "/v1/admin/speakers", map[string]any{
		"name": "Photo sonore " + word, "photo_id": sound.ID,
	}).Code)

	// renamed, the slug stays: the links of the apps keep working
	path := fmt.Sprintf("/v1/admin/speakers/%d", speaker.ID)
	renamed := send(t, router, http.MethodPut, admin.Token, path, map[string]any{
		"name": "Th. Andoseh " + word, "role": "Serviteur de Dieu", "bio": "Une courte biographie.",
	})
	require.Equal(t, http.StatusOK, renamed.Code, renamed.Body.String())
	updated := decode[adminTeachingSpeakerResponse](t, renamed)
	require.Equal(t, "Th. Andoseh "+word, updated.Name)
	require.Equal(t, speaker.Slug, updated.Slug)
	require.Equal(t, "Serviteur de Dieu", updated.Role)
	// the photo left aside
	require.Nil(t, updated.Photo)
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPut, admin.Token, path, map[string]any{
		"name": "",
	}).Code)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPut, admin.Token,
		fmt.Sprintf("/v1/admin/speakers/%d", speaker.ID+1_000_000), map[string]any{"name": "Personne"}).Code)

	// archived, a fiche leaves the forms but keeps its page and its name
	archived := send(t, router, http.MethodPut, admin.Token, path+"/archive", map[string]any{"archived": true})
	require.Equal(t, http.StatusOK, archived.Code, archived.Body.String())
	require.True(t, decode[adminTeachingSpeakerResponse](t, archived).Archived)
	found := speakerNamed(t, listedSpeakersFor(t, router, admin.Token), speaker.ID)
	require.True(t, found.Archived)
	// the same name can be used again while it is archived
	replacement := createSpeakerFor(t, router, admin.Token, map[string]any{"name": "Th. Andoseh " + word})
	require.NotEqual(t, speaker.ID, replacement.ID)
	// back, the name is taken: the other one has to be archived first
	require.Equal(t, http.StatusConflict, send(t, router, http.MethodPut, admin.Token, path+"/archive", map[string]any{
		"archived": false,
	}).Code)
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPut, admin.Token,
		fmt.Sprintf("/v1/admin/speakers/%d/archive", replacement.ID), map[string]any{"archived": true}).Code)
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPut, admin.Token, path+"/archive", map[string]any{
		"archived": false,
	}).Code)

	// a fiche no teaching names goes for good
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, admin.Token,
		fmt.Sprintf("/v1/admin/speakers/%d", replacement.ID), nil).Code)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodDelete, admin.Token,
		fmt.Sprintf("/v1/admin/speakers/%d", replacement.ID), nil).Code)
}

func TestTeachingsJoinTheirSpeaker(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)
	word := uniqueName()

	speaker := createSpeakerFor(t, router, admin.Token, map[string]any{
		"name": "Pasteur " + word, "role": "Pasteur",
	})
	audio := func() int64 {
		uploaded := uploadAudio(t, router, admin.Token, "message.mp3", mp3Bytes)
		require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
		return decode[teachingAudioResponse](t, uploaded).ID
	}

	// the new form sends the fiche
	chosen := createTeachingFor(t, router, admin.Token, map[string]any{
		"title": "La sainteté " + word, "speaker_id": speaker.ID,
		"audio_id": audio(), "duration_seconds": 1800, "published": true,
	})
	require.NotNil(t, chosen.SpeakerID)
	require.Equal(t, speaker.ID, *chosen.SpeakerID)
	require.Equal(t, speaker.Name, chosen.Speaker)
	require.Equal(t, speaker.Slug, chosen.SpeakerSlug)

	// an old app sends the name: the same fiche is found, not a second one
	typed := createTeachingFor(t, router, admin.Token, map[string]any{
		"title": "Le pardon " + word, "speaker": strings.ToLower(speaker.Name),
		"audio_id": audio(), "duration_seconds": 900, "published": true,
	})
	require.NotNil(t, typed.SpeakerID)
	require.Equal(t, speaker.ID, *typed.SpeakerID)
	require.Equal(t, speaker.Name, typed.Speaker)
	// an unknown id is refused
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPost, admin.Token, "/v1/admin/teachings", map[string]any{
		"title": "Personne " + word, "speaker_id": speaker.ID + 1_000_000, "audio_id": audio(),
	}).Code)

	// the admins see the fiche with its two teachings
	found := speakerNamed(t, listedSpeakersFor(t, router, admin.Token), speaker.ID)
	require.Equal(t, int32(2), found.TeachingCount)

	// the members filter by the fiche, and by the name as before
	byID := listedTeachings(t, router, boris.Token, fmt.Sprintf("?speaker_id=%d", speaker.ID))
	require.Equal(t, int64(2), byID.Meta.Total)
	byName := listedTeachings(t, router, boris.Token, "?speaker="+word)
	require.Equal(t, int64(2), byName.Meta.Total)
	require.Empty(t, listedTeachings(t, router, boris.Token, "?speaker=personne-"+word).Content)

	// the facets name the fiche, not a text
	facets := send(t, router, http.MethodGet, boris.Token, "/v1/teachings/facets", nil)
	require.Equal(t, http.StatusOK, facets.Code, facets.Body.String())
	values := decode[teachingFacetsResponse](t, facets)
	var facet *teachingSpeakerFacet
	for index := range values.Speakers {
		if values.Speakers[index].ID == speaker.ID {
			facet = &values.Speakers[index]
		}
	}
	require.NotNil(t, facet)
	require.Equal(t, speaker.Name, facet.Name)
	require.Equal(t, speaker.Slug, facet.Slug)
	require.Equal(t, 2, facet.Count)

	// the public list of the speakers counts the same two
	speakers := send(t, router, http.MethodGet, boris.Token, "/v1/teachings/speakers", nil)
	require.Equal(t, http.StatusOK, speakers.Code, speakers.Body.String())
	var listed *teachingSpeakerResponse
	for _, row := range decode[[]teachingSpeakerResponse](t, speakers) {
		if row.ID == speaker.ID {
			copied := row
			listed = &copied
		}
	}
	require.NotNil(t, listed)
	require.Equal(t, 2, listed.Count)

	// renamed, every teaching takes the new name at once, and the search too
	newName := "Apôtre " + word
	renamed := send(t, router, http.MethodPut, admin.Token, fmt.Sprintf("/v1/admin/speakers/%d", speaker.ID), map[string]any{
		"name": newName,
	})
	require.Equal(t, http.StatusOK, renamed.Code, renamed.Body.String())
	for _, teaching := range listedTeachings(t, router, boris.Token, fmt.Sprintf("?speaker_id=%d", speaker.ID)).Content {
		require.Equal(t, newName, teaching.Speaker)
	}
	searched := listedTeachings(t, router, boris.Token, "?search="+word+"%20apotre")
	require.Equal(t, int64(2), searched.Meta.Total)

	// a rename onto a name already taken is refused
	other := createSpeakerFor(t, router, admin.Token, map[string]any{"name": "Autre " + word})
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPut, admin.Token,
		fmt.Sprintf("/v1/admin/speakers/%d", other.ID), map[string]any{"name": "apotre " + word}).Code)
}

func TestDuplicateSpeakersAreMerged(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)
	word := uniqueName()

	kept := createSpeakerFor(t, router, admin.Token, map[string]any{"name": "Théodore " + word, "role": "Pasteur"})
	duplicate := createSpeakerFor(t, router, admin.Token, map[string]any{"name": "Th. " + word})
	require.NotEqual(t, kept.Slug, duplicate.Slug)

	audio := decode[teachingAudioResponse](t, uploadAudio(t, router, admin.Token, "x.mp3", mp3Bytes))
	teaching := createTeachingFor(t, router, admin.Token, map[string]any{
		"title": "Le message " + word, "speaker_id": duplicate.ID,
		"audio_id": audio.ID, "published": true,
	})
	require.NotNil(t, teaching.SpeakerID)
	require.Equal(t, duplicate.ID, *teaching.SpeakerID)

	// the teachings of the duplicate go to the one kept, and it is archived
	merged := send(t, router, http.MethodPost, admin.Token, "/v1/admin/speakers/merge", map[string]any{
		"into_id": kept.ID, "from_ids": []int64{duplicate.ID},
	})
	require.Equal(t, http.StatusOK, merged.Code, merged.Body.String())
	target := decode[adminTeachingSpeakerResponse](t, merged)
	require.Equal(t, kept.ID, target.ID)
	require.Equal(t, int32(1), target.TeachingCount)

	speakers := listedSpeakersFor(t, router, admin.Token)
	require.True(t, speakerNamed(t, speakers, duplicate.ID).Archived)
	require.Zero(t, speakerNamed(t, speakers, duplicate.ID).TeachingCount)
	require.False(t, speakerNamed(t, speakers, kept.ID).Archived)

	// the teaching now names the fiche kept, in its text too
	page := listedTeachings(t, router, boris.Token, "?speaker_id="+fmt.Sprint(kept.ID))
	require.Equal(t, int64(1), page.Meta.Total)
	require.Equal(t, kept.Name, page.Content[0].Speaker)
	require.Equal(t, kept.Slug, page.Content[0].SpeakerSlug)
	require.Empty(t, listedTeachings(t, router, boris.Token, "?speaker_id="+fmt.Sprint(duplicate.ID)).Content)

	// the archived duplicate is out of the members' list
	public := send(t, router, http.MethodGet, boris.Token, "/v1/teachings/speakers", nil)
	require.Equal(t, http.StatusOK, public.Code, public.Body.String())
	for _, row := range decode[[]teachingSpeakerResponse](t, public) {
		require.NotEqual(t, duplicate.ID, row.ID)
	}

	// nothing to merge, an unknown fiche, an archived target: all refused
	for _, body := range []map[string]any{
		{"into_id": kept.ID},
		{"into_id": kept.ID, "from_ids": []int64{kept.ID + 1_000_000}},
		{"into_id": kept.ID + 1_000_000, "from_ids": []int64{duplicate.ID}},
		{"into_id": duplicate.ID, "from_ids": []int64{kept.ID}},
	} {
		require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPost, admin.Token,
			"/v1/admin/speakers/merge", body).Code, "%v", body)
	}

	// a fiche still named by a teaching is archived, never deleted
	require.Equal(t, http.StatusConflict, send(t, router, http.MethodDelete, admin.Token,
		fmt.Sprintf("/v1/admin/speakers/%d", kept.ID), nil).Code)
	// the archived duplicate names nothing any more: it can go
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, admin.Token,
		fmt.Sprintf("/v1/admin/speakers/%d", duplicate.ID), nil).Code)
}
