package media

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTheStorageOfTheConfig(t *testing.T) {
	// no bucket: the folder
	store, err := NewStore(t.TempDir(), S3Config{})
	require.NoError(t, err)
	require.IsType(t, &DiskStore{}, store)

	// neither: refused rather than lost
	_, err = NewStore("", S3Config{})
	require.Error(t, err)

	// a bucket with a wrong address: said at once
	_, err = NewStore("", S3Config{Bucket: "media", Endpoint: "not an address"})
	require.ErrorContains(t, err, "S3_ENDPOINT")
}

func TestTheTypeOfAFileInTheBucket(t *testing.T) {
	require.Equal(t, "image/jpeg", contentTypeOf("3f91ab.jpg"))
	require.Equal(t, "image/png", contentTypeOf("3f91ab.png"))
	require.Equal(t, "application/pdf", contentTypeOf("3f91ab.pdf"))
	require.Equal(t, "application/octet-stream", contentTypeOf("3f91ab"))
}
