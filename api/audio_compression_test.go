package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/media"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/stretchr/testify/require"
)

// fakeCompressor gives [out] (or [err]) for any sound.
type fakeCompressor struct {
	out []byte
	err error
}

func (f fakeCompressor) Compress(context.Context, []byte) ([]byte, error) {
	return f.out, f.err
}

// smallM4A: a small M4A, as ffmpeg gives it.
var smallM4A = append([]byte{0, 0, 0, 0x14}, []byte("ftypM4A \x00\x00\x00\x00M4A ")...)

func newCompressingRouter(t *testing.T, compressor media.AudioCompressor) (http.Handler, *Server) {
	t.Helper()
	server, err := New(testStore, &utils.Config{
		Token: utils.TokenConfig{SecretKey: strings.Repeat("k", 32), Duration: "15m"},
	})
	require.NoError(t, err)
	server.audio = compressor
	return server.SetupRouter(), server
}

func TestASoundIsCompressedAfterItWasSent(t *testing.T) {
	router, server := newCompressingRouter(t, fakeCompressor{out: smallM4A})
	admin := staffAdmin(t, router)

	// an MP3 of a few kilobytes, sent as is
	sound := append(append([]byte{}, mp3Bytes...), bytes.Repeat([]byte{0}, 4000)...)
	uploaded := uploadAudio(t, router, admin.Token, "La croix.mp3", sound)
	require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
	audio := decode[teachingAudioResponse](t, uploaded)
	require.Equal(t, "mp3", audio.Format)

	// then re-encoded smaller, under the same media
	server.compressing.Wait()
	rows, err := testStore.ListMediaByIDs(context.Background(), []int64{audio.ID})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, media.M4A, rows[0].ContentType)
	require.Equal(t, int64(len(smallM4A)), rows[0].SizeBytes)
	require.True(t, strings.HasSuffix(rows[0].Key, ".m4a"))
	served := send(t, router, http.MethodGet, "", mediaURL(rows[0].Key), nil)
	require.Equal(t, http.StatusOK, served.Code)
	require.Equal(t, smallM4A, served.Body.Bytes())
	// the first file is gone
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodGet, "", audio.URL, nil).Code)
}

func TestASoundIsKeptWhenCompressingDoesntHelp(t *testing.T) {
	for _, compressor := range []fakeCompressor{
		{err: errors.New("ffmpeg crashed")},
		// hardly smaller
		{out: append(append([]byte{}, smallM4A...), bytes.Repeat([]byte{0}, 30)...)},
	} {
		router, server := newCompressingRouter(t, compressor)
		admin := staffAdmin(t, router)
		uploaded := uploadAudio(t, router, admin.Token, "La croix.mp3", mp3Bytes)
		require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
		audio := decode[teachingAudioResponse](t, uploaded)

		server.compressing.Wait()
		rows, err := testStore.ListMediaByIDs(context.Background(), []int64{audio.ID})
		require.NoError(t, err)
		require.Equal(t, media.MP3, rows[0].ContentType)
		require.Equal(t, http.StatusOK, send(t, router, http.MethodGet, "", audio.URL, nil).Code)
	}
}
