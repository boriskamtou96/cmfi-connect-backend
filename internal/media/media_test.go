package media

import (
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
