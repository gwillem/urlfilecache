//go:build integration

package urlfilecache

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTorIgnoresGzipETag(t *testing.T) {
	// Log = log.New(os.Stderr, "urlfilecache ", log.LstdFlags)
	url := "https://check.torproject.org/torbulkexitlist"

	path, err := ToPath(url)
	require.NoError(t, err)
	initialMtime := getMtime(path)

	require.NoError(t, os.Truncate(path, 0))
	require.NoError(t, os.Chtimes(path, initialMtime, initialMtime))

	// Second request - should get 304 Not Modified due to If-Modified-Since/ETag
	err = ToCustomPath(url, path)
	require.NoError(t, err)

	// Verify file is empty after second request (it should NOT have been updated)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, int64(0), info.Size())
}
