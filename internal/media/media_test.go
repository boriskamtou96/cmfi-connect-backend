package media

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// picture is an image of one color.
func picture(width, height int, fill color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, fill)
		}
	}
	return img
}

func pngOf(t *testing.T, img image.Image) []byte {
	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, img))
	return buffer.Bytes()
}

// phoneJPEG is a JPEG as a phone writes it: with an EXIF block (the place).
func phoneJPEG(t *testing.T, img image.Image, exif string) []byte {
	var buffer bytes.Buffer
	require.NoError(t, jpeg.Encode(&buffer, img, nil))
	data := buffer.Bytes()
	payload := append([]byte("Exif\x00\x00"), exif...)
	length := len(payload) + 2
	segment := append([]byte{0xFF, 0xE1, byte(length >> 8), byte(length)}, payload...)
	// right after the start of image (FF D8)
	return append(append(append([]byte{}, data[:2]...), segment...), data[2:]...)
}

func TestAvatarIsMadeAgainAsAJPEGWithoutTheMetadata(t *testing.T) {
	const place = "GPSLatitude 3.8480 GPSLongitude 11.5021"
	sent := phoneJPEG(t, picture(300, 300, color.RGBA{R: 200, G: 30, B: 30, A: 255}), place)
	_, _, err := image.Decode(bytes.NewReader(sent))
	require.NoError(t, err, "the phone JPEG of the test must be readable")
	require.Contains(t, string(sent), place)

	data, info, err := Avatar(sent)
	require.NoError(t, err)
	require.Equal(t, "image/jpeg", http.DetectContentType(data))
	require.NotContains(t, string(data), place)
	require.NotContains(t, string(data), "Exif")
	require.Equal(t, Info{ContentType: "image/jpeg", Width: 300, Height: 300, Color: info.Color, Ext: ".jpg"}, info)
	require.Regexp(t, `^#[0-9A-F]{6}$`, info.Color)

	decoded, err := jpeg.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 300, 300), decoded.Bounds())
}

func TestAvatarFromAPNG(t *testing.T) {
	// almost square is square enough (a crop rounded by the phone)
	data, info, err := Avatar(pngOf(t, picture(202, 200, color.RGBA{B: 255, A: 255})))
	require.NoError(t, err)
	require.Equal(t, "image/jpeg", http.DetectContentType(data))
	require.Equal(t, 202, info.Width)
	require.Equal(t, 200, info.Height)
	require.Equal(t, ".jpg", info.Ext)

	// a transparent background turns white, not black
	_, info, err = Avatar(pngOf(t, picture(200, 200, color.Transparent)))
	require.NoError(t, err)
	require.Equal(t, "#FFFFFF", info.Color)
}

func TestAvatarRefusals(t *testing.T) {
	blue := color.RGBA{B: 255, A: 255}
	cases := map[string]struct {
		data []byte
		want error
	}{
		"not square":   {pngOf(t, picture(300, 200, blue)), ErrAvatarShape},
		"3 px off":     {pngOf(t, picture(203, 200, blue)), ErrAvatarShape},
		"too small":    {pngOf(t, picture(100, 100, blue)), ErrAvatarSize},
		"too large":    {pngOf(t, image.NewGray(image.Rect(0, 0, MaxAvatarSide+1, MaxAvatarSide+1))), ErrAvatarSize},
		"a PDF":        {[]byte("%PDF-1.4\n%%EOF\n"), ErrUnsupported},
		"text":         {[]byte("hello"), ErrUnsupported},
		"broken PNG":   {pngOf(t, picture(200, 200, blue))[:60], ErrUnsupported},
		"PNG no frame": {[]byte("\x89PNG\r\n\x1a\n"), ErrUnsupported},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := Avatar(c.data)
			require.ErrorIs(t, err, c.want)
		})
	}
}

func TestDiskStoreDeletesAFile(t *testing.T) {
	store, err := NewDiskStore(t.TempDir())
	require.NoError(t, err)
	key := NewKey(".jpg")
	require.NoError(t, store.Save(key, []byte("photo")))

	require.NoError(t, store.Delete(key))
	_, err = os.Stat(filepath.Join(store.Dir, key))
	require.ErrorIs(t, err, os.ErrNotExist)
	// already gone: nothing to do
	require.NoError(t, store.Delete(key))
	// never outside the folder
	require.Error(t, store.Delete("../media.go"))
}

func TestInspectDocument(t *testing.T) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	file, err := archive.Create("word/document.xml")
	require.NoError(t, err)
	_, err = file.Write([]byte("<w:document/>"))
	require.NoError(t, err)
	require.NoError(t, archive.Close())

	info, err := InspectDocument(buffer.Bytes(), "Règlement.docx")
	require.NoError(t, err)
	require.Equal(t, Info{ContentType: DOCX, Ext: ".docx"}, info)
	require.True(t, info.IsDocument())

	info, err = InspectDocument([]byte("%PDF-1.4\n%%EOF\n"), "cours.pdf")
	require.NoError(t, err)
	require.Equal(t, PDF, info.ContentType)

	ole := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 32)...)
	info, err = InspectDocument(ole, "ancien.DOC")
	require.NoError(t, err)
	require.Equal(t, DOC, info.ContentType)
	// the same container holds Excel files
	_, err = InspectDocument(ole, "tableau.xls")
	require.ErrorIs(t, err, ErrUnsupportedDocument)

	// a zip that is not a Word file
	buffer.Reset()
	archive = zip.NewWriter(&buffer)
	_, err = archive.Create("photos/1.jpg")
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	_, err = InspectDocument(buffer.Bytes(), "photos.docx")
	require.ErrorIs(t, err, ErrUnsupportedDocument)

	// Inspect, for posters and photos, still refuses a Word file
	_, err = Inspect(ole)
	require.ErrorIs(t, err, ErrUnsupported)
	require.True(t, ValidKey(NewKey(".docx")))
}

func TestInspectAudio(t *testing.T) {
	for name, data := range map[string][]byte{
		// an ID3 tag, or straight the first frame (MPEG 1 and 2, layer III)
		"tag.mp3":   []byte("ID3\x04\x00\x00\x00\x00\x00\x00"),
		"mpeg1.mp3": {0xFF, 0xFB, 0x90, 0x64},
		"mpeg2.mp3": {0xFF, 0xF3, 0x90, 0x64},
		"sans nom":  {0xFF, 0xFA, 0x90, 0x64},
	} {
		info, err := InspectAudio(data, name)
		require.NoError(t, err, name)
		require.Equal(t, Info{ContentType: MP3, Ext: ".mp3"}, info, name)
		require.True(t, info.IsAudio())
	}

	m4a := append([]byte{0, 0, 0, 0x20}, []byte("ftypM4A \x00\x00\x00\x00")...)
	info, err := InspectAudio(m4a, "message")
	require.NoError(t, err)
	require.Equal(t, Info{ContentType: M4A, Ext: ".m4a"}, info)
	video := append([]byte{0, 0, 0, 0x20}, []byte("ftypisom\x00\x00\x02\x00")...)
	_, err = InspectAudio(video, "film.mp4")
	require.ErrorIs(t, err, ErrUnsupportedAudio)
	// an MP4 the admin calls a sound
	info, err = InspectAudio(video, "message.M4A")
	require.NoError(t, err)
	require.Equal(t, M4A, info.ContentType)

	for name, data := range map[string][]byte{
		// AAC without its box (layer bits 00), MPEG layer II, a PDF, nothing
		"son.aac": {0xFF, 0xF1, 0x50, 0x80},
		"son.mp2": {0xFF, 0xFD, 0x90, 0x64},
		"x.pdf":   []byte("%PDF-1.4\n"),
		"vide":    {},
	} {
		_, err := InspectAudio(data, name)
		require.ErrorIs(t, err, ErrUnsupportedAudio, name)
	}
	// a sound is never an image
	_, err = Inspect([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"))
	require.ErrorIs(t, err, ErrUnsupported)
	require.True(t, ValidKey(NewKey(".mp3")))
	require.True(t, ValidKey(NewKey(".m4a")))
}
