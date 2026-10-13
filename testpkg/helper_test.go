package testpkg

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCacheFilesInSameDir verifies that data, etag, and checked files all land in
// the same directory when UsePackagePath is used, regardless of internal call depth.
func TestCacheFilesInSameDir(t *testing.T) {
	const testETag = `"test-etag-123"`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == testETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", testETag)
		w.Header().Set("Last-Modified", "Wed, 09 Apr 2026 00:00:00 GMT")
		_, _ = w.Write([]byte("test content"))
	}))
	defer ts.Close()

	path, err := FetchWithPackageName(ts.URL)
	require.NoError(t, err)
	defer os.Remove(path) //nolint:errcheck

	dataDir := filepath.Dir(path)

	// Find all cache files for this URL in the expected directory
	entries, err := os.ReadDir(dataDir)
	require.NoError(t, err)

	base := strings.TrimSuffix(filepath.Base(path), ".data")

	var foundEtag, foundChecked bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), base) {
			switch {
			case strings.HasSuffix(e.Name(), ".etag"):
				foundEtag = true
				content, err := os.ReadFile(filepath.Join(dataDir, e.Name()))
				require.NoError(t, err)
				require.Equal(t, testETag, string(content))
				defer os.Remove(filepath.Join(dataDir, e.Name())) //nolint:errcheck
			case strings.HasSuffix(e.Name(), ".checked"):
				foundChecked = true
				defer os.Remove(filepath.Join(dataDir, e.Name())) //nolint:errcheck
			}
		}
	}

	require.True(t, foundEtag, "etag file should be in same dir as data file (%s)", dataDir)
	require.True(t, foundChecked, "checked file should be in same dir as data file (%s)", dataDir)

	// Verify the directory uses the package name, not "urlfilecache"
	require.Contains(t, strings.Split(dataDir, "/"), "testpkg",
		"cache dir should use caller's package name")
	require.NotContains(t, strings.Split(dataDir, "/"), "urlfilecache",
		"cache dir should NOT use urlfilecache's own package name")
}

func TestUsePackageName(t *testing.T) {
	// Create a test server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("test content"))
	}))
	defer ts.Close()

	// Get path with UsePackageName (should use "testpkg" as the package name)
	packagePath, err := FetchWithPackageName(ts.URL)
	require.NoError(t, err)
	defer os.Remove(packagePath) //nolint:errcheck

	// Get path without UsePackageName (should use executable name)
	defaultPath, err := FetchDefault(ts.URL)
	require.NoError(t, err)
	defer os.Remove(defaultPath) //nolint:errcheck

	execName := filepath.Base(os.Args[0])
	require.Contains(t, strings.Split(packagePath, "/"), "testpkg")
	require.NotContains(t, packagePath, execName, "path should not contain executable name")
	require.Contains(t, strings.Split(defaultPath, "/"), execName, "default path should contain executable name")

	// Paths should be different (unless executable is coincidentally named "testpkg")
	if execName != "testpkg" {
		require.NotEqual(t, defaultPath, packagePath, "paths should differ when using UsePackageName")
	}

	// Both files should exist and have the same content
	defaultContent, err := os.ReadFile(defaultPath)
	require.NoError(t, err)
	require.Equal(t, "test content", string(defaultContent))

	packageContent, err := os.ReadFile(packagePath)
	require.NoError(t, err)
	require.Equal(t, "test content", string(packageContent))
}
