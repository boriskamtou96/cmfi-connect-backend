package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTeachingsAreFilteredByCategoryYearAndDay(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	member := registerMember(t, router, nil)

	// a category the admins create
	name := "Séminaire des anciens " + uniqueName()
	createdCategory := send(t, router, http.MethodPost, admin.Token, "/v1/admin/teaching-categories", map[string]any{
		"name": name, "annual": true, "description": "Chaque année",
	})
	require.Equal(t, http.StatusCreated, createdCategory.Code, createdCategory.Body.String())
	category := decode[adminTeachingCategoryResponse](t, createdCategory)
	require.Contains(t, category.Slug, "seminaire-des-anciens")
	require.True(t, category.Annual)

	// its edition for 2026
	createdEdition := send(t, router, http.MethodPost, admin.Token, "/v1/admin/teaching-editions", map[string]any{
		"category_id": category.ID, "year": 2026, "title": "Séminaire des anciens 2026",
		"theme": "La sainteté", "starts_on": "2026-08-01", "ends_on": "2026-08-03",
	})
	require.Equal(t, http.StatusCreated, createdEdition.Code, createdEdition.Body.String())
	edition := decode[teachingEditionResponse](t, createdEdition)
	require.Equal(t, int32(2026), edition.Year)

	audio := decode[teachingAudioResponse](t, uploadAudio(t, router, admin.Token, "jour.mp3", mp3Bytes))
	// two messages on the first day, in a chosen order, and one on the second
	first := createTeachingFor(t, router, admin.Token, map[string]any{
		"title": "La sainteté", "speaker": "Théodore Andoseh", "category_id": category.ID,
		"edition_id": edition.ID, "day_number": 1, "position": 1, "published": true,
		"audio_id": audio.ID,
	})
	second := createTeachingFor(t, router, admin.Token, map[string]any{
		"title": "Le pardon", "speaker": "Théodore Andoseh", "category_id": category.ID,
		"edition_id": edition.ID, "day_number": 1, "position": 2, "published": true,
		"audio_id": audio.ID,
	})
	third := createTeachingFor(t, router, admin.Token, map[string]any{
		"title": "La mission", "speaker": "Un autre orateur", "category_id": category.ID,
		"edition_id": edition.ID, "day_number": 2, "position": 1, "published": true,
		"audio_id": audio.ID,
	})
	require.Equal(t, name, first.Category)
	require.Equal(t, int32(2026), *first.Year)
	require.Equal(t, int32(1), *first.DayNumber)
	require.NotNil(t, first.Edition)

	// the first day of the 2026 edition, by its category slug, its year, its
	// day and its speaker: both messages, in the order chosen
	dayOne := listedTeachings(t, router, member.Token,
		fmt.Sprintf("?category=%s&year=2026&day=1&speaker=andoseh", category.Slug))
	require.Equal(t, int64(2), dayOne.Meta.Total)
	require.Equal(t, first.ID, dayOne.Content[0].ID)
	require.Equal(t, second.ID, dayOne.Content[1].ID)

	// a search over the words and the year, with the speaker
	search := listedTeachings(t, router, member.Token, "?q=pardon%20andoseh&year=2026")
	require.Equal(t, int64(1), search.Meta.Total)
	require.Equal(t, second.ID, search.Content[0].ID)

	// the second day, another speaker
	dayTwo := listedTeachings(t, router, member.Token,
		fmt.Sprintf("?edition_id=%d&day=2", edition.ID))
	require.Equal(t, int64(1), dayTwo.Meta.Total)
	require.Equal(t, third.ID, dayTwo.Content[0].ID)

	// the values and the counts of the filters
	facets := send(t, router, http.MethodGet, member.Token,
		fmt.Sprintf("/v1/teachings/facets?edition_id=%d", edition.ID), nil)
	require.Equal(t, http.StatusOK, facets.Code, facets.Body.String())
	values := decode[teachingFacetsResponse](t, facets)
	require.Contains(t, values.Categories, teachingCategoryResponse{
		ID: category.ID, Name: name, Slug: category.Slug, Count: 3,
	})
	require.Contains(t, values.Speakers, teachingSpeakerFacet{Name: "Théodore Andoseh", Count: 2})
	require.Contains(t, values.Years, teachingYearFacet{Year: 2026, Count: 3})
	require.Contains(t, values.Editions, teachingEditionFacet{
		ID: edition.ID, CategoryID: category.ID, CategoryName: name,
		CategorySlug: category.Slug, Year: 2026, Title: "Séminaire des anciens 2026", Count: 3,
	})
	require.Contains(t, values.Days, teachingDayFacet{Day: 1, Count: 2})
	require.Contains(t, values.Days, teachingDayFacet{Day: 2, Count: 1})

	// renamed: the slug stays (the apps' links keep working) and the teachings
	// show the new name
	renamed := send(t, router, http.MethodPut, admin.Token,
		fmt.Sprintf("/v1/admin/teaching-categories/%d", category.ID), map[string]any{"name": name + " 2026"})
	require.Equal(t, http.StatusOK, renamed.Code, renamed.Body.String())
	require.Equal(t, category.Slug, decode[adminTeachingCategoryResponse](t, renamed).Slug)
	require.Equal(t, name+" 2026", listedTeachings(t, router, member.Token,
		fmt.Sprintf("?edition_id=%d", edition.ID)).Content[0].Category)

	// an archived category keeps its teachings but leaves the public chips
	archived := send(t, router, http.MethodPut, admin.Token,
		fmt.Sprintf("/v1/admin/teaching-categories/%d/archive", category.ID), map[string]any{"archived": true})
	require.Equal(t, http.StatusOK, archived.Code, archived.Body.String())
	categories := send(t, router, http.MethodGet, member.Token, "/v1/teachings/categories", nil)
	require.Equal(t, http.StatusOK, categories.Code)
	for _, one := range decode[[]teachingCategoryResponse](t, categories) {
		require.NotEqual(t, category.ID, one.ID)
	}
	require.Equal(t, int64(2), listedTeachings(t, router, member.Token,
		fmt.Sprintf("?category_id=%d&day=1", category.ID)).Meta.Total)

	// an edition with teachings can't be deleted
	conflict := send(t, router, http.MethodDelete, admin.Token,
		fmt.Sprintf("/v1/admin/teaching-editions/%d", edition.ID), nil)
	require.Equal(t, http.StatusConflict, conflict.Code)
}
