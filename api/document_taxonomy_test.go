package api

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDocumentsAreFilteredByCategoryAndSearch(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	member := registerMember(t, router, nil)

	// a category the admins create
	name := "Témoignages " + uniqueName()
	createdCategory := send(t, router, http.MethodPost, admin.Token, "/v1/admin/document-categories", map[string]any{
		"name": name, "description": "Les récits des membres",
	})
	require.Equal(t, http.StatusCreated, createdCategory.Code, createdCategory.Body.String())
	category := decode[adminDocumentCategoryResponse](t, createdCategory)
	require.Contains(t, category.Slug, "temoignages")
	require.False(t, category.Archived)

	file := decode[libraryUploadResponse](t, uploadDocument(t, router, admin.Token, "recit.pdf", pdfBytes))
	document := createLibraryDocument(t, router, admin.Token, map[string]any{
		"title": "Guéri à Douala", "description": "Le récit d'une guérison",
		"category_id": category.ID, "file_id": file.ID, "file_name": file.Name, "published": true,
	})
	require.Equal(t, int64(category.ID), *document.CategoryID)
	require.Equal(t, category.Slug, document.CategorySlug)
	// the old text column follows the category, for the installed apps
	require.Equal(t, name, document.Category)

	// by its slug, its name, its id, and by a search over the words
	for _, query := range []string{
		"?category=" + category.Slug,
		"?category=" + url.QueryEscape(name),
		fmt.Sprintf("?category_id=%d", category.ID),
		"?search=gueri%20dOUALA",
	} {
		page := listedDocuments(t, router, member.Token, query)
		require.Equal(t, int64(1), page.Meta.Total, query)
		require.Equal(t, document.ID, page.Content[0].ID, query)
	}
	require.Empty(t, listedDocuments(t, router, member.Token, "?category=Formulaires").Content)

	// the public chips, with their counts
	categories := send(t, router, http.MethodGet, member.Token, "/v1/documents/categories", nil)
	require.Equal(t, http.StatusOK, categories.Code)
	require.Contains(t, decode[[]libraryCategoryResponse](t, categories), libraryCategoryResponse{
		ID: category.ID, Name: name, Slug: category.Slug, Count: 1,
	})

	// renamed: its documents keep the new name, without the admins touching them
	renamed := send(t, router, http.MethodPut, admin.Token,
		fmt.Sprintf("/v1/admin/document-categories/%d", category.ID), map[string]any{"name": name + " vécus"})
	require.Equal(t, http.StatusOK, renamed.Code, renamed.Body.String())
	require.Equal(t, name+" vécus", decode[adminDocumentCategoryResponse](t, renamed).Name)
	require.Equal(t, name+" vécus", listedDocuments(t, router, member.Token,
		"?category="+category.Slug).Content[0].Category)

	// the same name can't be used twice
	duplicate := send(t, router, http.MethodPost, admin.Token, "/v1/admin/document-categories", map[string]any{
		"name": name + " vécus",
	})
	require.Equal(t, http.StatusBadRequest, duplicate.Code)

	// a name typed by hand in an old form becomes a category
	typed := createLibraryDocument(t, router, admin.Token, map[string]any{
		"title": "Une prière", "category": "Prières " + uniqueName(), "file_id": file.ID, "published": true,
	})
	require.NotNil(t, typed.CategoryID)

	// an archived category keeps its documents but leaves the public chips
	archived := send(t, router, http.MethodPut, admin.Token,
		fmt.Sprintf("/v1/admin/document-categories/%d/archive", category.ID), map[string]any{"archived": true})
	require.Equal(t, http.StatusOK, archived.Code, archived.Body.String())
	require.True(t, decode[adminDocumentCategoryResponse](t, archived).Archived)
	chips := send(t, router, http.MethodGet, member.Token, "/v1/documents/categories", nil)
	require.Equal(t, http.StatusOK, chips.Code)
	for _, one := range decode[[]libraryCategoryResponse](t, chips) {
		require.NotEqual(t, category.ID, one.ID)
	}
	require.Equal(t, int64(1), listedDocuments(t, router, member.Token, "?category="+category.Slug).Meta.Total)

	// the admins still see it, with its documents
	adminCategories := send(t, router, http.MethodGet, admin.Token, "/v1/admin/document-categories", nil)
	require.Equal(t, http.StatusOK, adminCategories.Code)
	savedCategories := decode[[]adminDocumentCategoryResponse](t, adminCategories)
	var archivedCategory *adminDocumentCategoryResponse
	for index := range savedCategories {
		if savedCategories[index].ID == category.ID {
			archivedCategory = &savedCategories[index]
		}
	}
	require.NotNil(t, archivedCategory)
	require.True(t, archivedCategory.Archived)
	require.Equal(t, int32(1), archivedCategory.DocumentCount)

	// an archived category frees its name
	reused := send(t, router, http.MethodPost, admin.Token, "/v1/admin/document-categories", map[string]any{
		"name": name + " vécus",
	})
	require.Equal(t, http.StatusCreated, reused.Code, reused.Body.String())
}
