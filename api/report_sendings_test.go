package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type sendingsPage struct {
	Content []reportSendingResponse `json:"content"`
	Meta    MetaResponse            `json:"meta"`
}

func sendingsOf(t *testing.T, router http.Handler, token, path string) sendingsPage {
	t.Helper()
	response := get(t, router, token, path, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page sendingsPage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page), response.Body.String())
	require.NotNil(t, page.Content, "nothing is [], not null")
	return page
}

func sendPeriod(t *testing.T, router http.Handler, token string, body map[string]any) []reportSendingResponse {
	t.Helper()
	response := send(t, router, http.MethodPost, token, "/v1/report-sendings", body)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	return decode[[]reportSendingResponse](t, response)
}

func TestADiscipleSendsTheWeekToTheirAuthorityWhoAnswers(t *testing.T) {
	router, sender := newPushRouter(t, "")
	grace := registerMember(t, router, map[string]string{"first_name": "Grâce"})
	paul := registerMember(t, router, map[string]string{"first_name": "Paul"})
	addPhone(t, router, paul, "paul-phone-"+paul.Username)
	addPhone(t, router, grace, "grace-phone-"+grace.Username)
	link := chooseDiscipleMaker(t, router, grace, paul)
	// the pushes of the request and its answer are not what is looked at
	sender.mu.Lock()
	sender.messages, sender.tokens = nil, nil
	sender.mu.Unlock()

	week := map[string]any{"from": daysAgo(6), "to": daysAgo(0)}
	sent := sendPeriod(t, router, grace.Token, week)
	require.Len(t, sent, 1)
	require.Equal(t, link.ID, sent[0].AuthorityID)
	require.Equal(t, paul.Username, sent[0].Member.Username)
	require.Nil(t, sent[0].ReadAt)

	// Paul's phone: « Grâce t'a envoyé son compte rendu »
	require.Len(t, sender.messages, 1)
	require.Equal(t, "Grâce t'a envoyé son compte rendu", sender.messages[0].Title)
	require.Equal(t, "report_sending", sender.messages[0].Data["type"])
	require.Equal(t, fmt.Sprint(sent[0].ID), sender.messages[0].Data["id"])

	received := sendingsOf(t, router, paul.Token, "/v1/report-sendings/received")
	require.Equal(t, int64(1), received.Meta.Total)
	require.Equal(t, grace.Username, received.Content[0].Member.Username)
	require.Equal(t, daysAgo(6), received.Content[0].PeriodFrom)
	// the period opens with the usual reports of the disciple
	require.Equal(t, http.StatusOK, discipleReports(t, router, paul.Token, link.ID,
		fmt.Sprintf("?from=%s&to=%s", daysAgo(6), daysAgo(0))).Code)

	path := fmt.Sprintf("/v1/report-sendings/%d", sent[0].ID)
	// only Paul reads it and answers it
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPost, grace.Token, path+"/read", nil).Code)
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodPost, paul.Token, path+"/read", nil).Code)
	require.NotNil(t, sendingsOf(t, router, paul.Token, "/v1/report-sendings/received").Content[0].ReadAt)

	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPost, paul.Token, path+"/reply",
		map[string]string{"text": "  "}).Code)
	replied := send(t, router, http.MethodPost, paul.Token, path+"/reply",
		map[string]string{"text": " Belle fidélité, continue ! "})
	require.Equal(t, http.StatusOK, replied.Code, replied.Body.String())
	require.Equal(t, "Belle fidélité, continue !", decode[reportSendingResponse](t, replied).Reply)

	// Grâce's phone: « Paul t'a répondu »
	require.Len(t, sender.messages, 2)
	require.Equal(t, "Paul t'a répondu", sender.messages[1].Title)
	require.Equal(t, "Belle fidélité, continue !", sender.messages[1].Body)
	require.Equal(t, "report_reply", sender.messages[1].Data["type"])

	mine := sendingsOf(t, router, grace.Token, "/v1/report-sendings/sent")
	require.Equal(t, "Belle fidélité, continue !", mine.Content[0].Reply)
	require.NotNil(t, mine.Content[0].RepliedAt)

	// sent again: new again, the answer kept, still one
	again := sendPeriod(t, router, grace.Token, week)
	require.Equal(t, sent[0].ID, again[0].ID)
	require.Nil(t, again[0].ReadAt)
	require.Equal(t, "Belle fidélité, continue !", again[0].Reply)
	require.Equal(t, int64(1), sendingsOf(t, router, paul.Token, "/v1/report-sendings/received").Meta.Total)
}

func TestOnlyTheAuthoritiesWhoCanReceiveItGetIt(t *testing.T) {
	router := newTestRouter(t)
	grace := registerMember(t, router, nil)
	paul := registerMember(t, router, nil)
	marie := registerMember(t, router, nil)
	pierre := registerMember(t, router, nil)
	toPaul := chooseDiscipleMaker(t, router, grace, paul)
	toMarie := chooseAuthority(t, router, grace, marie, false)
	// Pierre was asked but didn't accept yet
	asked := send(t, router, http.MethodPost, grace.Token, "/v1/authorities/",
		map[string]any{"username": pierre.Username})
	require.Equal(t, http.StatusCreated, asked.Code, asked.Body.String())
	// Marie no longer gets the reports
	off := send(t, router, http.MethodPut, grace.Token, fmt.Sprintf("/v1/authorities/%d/sharing", toMarie.ID),
		map[string]any{"shares": false})
	require.Equal(t, http.StatusOK, off.Code, off.Body.String())

	sent := sendPeriod(t, router, grace.Token, map[string]any{"from": daysAgo(13), "to": daysAgo(0)})
	require.Len(t, sent, 1)
	require.Equal(t, toPaul.ID, sent[0].AuthorityID)
	require.Empty(t, sendingsOf(t, router, marie.Token, "/v1/report-sendings/received").Content)
	require.Empty(t, sendingsOf(t, router, pierre.Token, "/v1/report-sendings/received").Content)

	// some of them only: one not among those who can receive it gets nothing
	require.Empty(t, sendPeriod(t, router, grace.Token, map[string]any{
		"from": daysAgo(1), "to": daysAgo(0), "authority_ids": []int64{toMarie.ID},
	}))
}

func TestAPeriodSentIsChecked(t *testing.T) {
	router := newTestRouter(t)
	grace := registerMember(t, router, nil)
	for _, body := range []map[string]any{
		{"from": daysAgo(0)},
		{"from": daysAgo(0), "to": daysAgo(3)},
		{"from": daysAgo(40), "to": daysAgo(0)},
		{"from": daysAgo(0), "to": today().AddDate(0, 0, 5).Format(reportDateLayout)},
		{"from": "18/10/2026", "to": daysAgo(0)},
	} {
		response := send(t, router, http.MethodPost, grace.Token, "/v1/report-sendings", body)
		require.Equal(t, http.StatusBadRequest, response.Code, "%v: %s", body, response.Body.String())
	}
	// nobody to send to: nothing, not an error
	require.Empty(t, sendPeriod(t, router, grace.Token, map[string]any{"from": daysAgo(6), "to": daysAgo(0)}))
	require.Equal(t, "Paul answered you", titleIn(reportReplyTitles, "en-GB", "Paul"))
	require.Equal(t, "Paul t'a répondu", titleIn(reportReplyTitles, "sw", "Paul"))
}
