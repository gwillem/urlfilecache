package urlfilecache

import (
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrg/xdg"

	"github.com/stretchr/testify/require"
)

var ts *httptest.Server

// toCustomPath is a test helper that allows specifying a custom path
func toCustomPath(url, path string, opts ...Option) error {
	opts = append(opts, WithPath(path))
	_, err := ToPath(url, opts...)
	return err
}

func TestMain(m *testing.M) {
	// Setup code
	dummyTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ifModSince := r.Header.Get("If-Modified-Since"); ifModSince != "" {
			ifModSinceTime, err := http.ParseTime(ifModSince)
			if err == nil && !dummyTime.After(ifModSinceTime) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		w.Header().Set("Last-Modified", dummyTime.Format(http.TimeFormat))
		_, _ = w.Write([]byte("test content"))
	}))

	// Run tests
	code := m.Run()

	// Teardown code
	ts.Close()

	os.Exit(code)
}

func TestURLFileCache(t *testing.T) {
	path, err := ToPath(ts.URL)
	defer os.Remove(path)
	require.NoError(t, err)

	// Verify file exists and contains expected content
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "test content", string(content))
}

func TestToCustomPath(t *testing.T) {
	f, err := os.CreateTemp("", "urlfilecache")
	if err != nil {
		log.Fatal(err)
	}
	defer os.Remove(f.Name())
	dst := f.Name()

	// try A
	require.NoError(t, toCustomPath(ts.URL, dst))

	timeA := getMtime(dst)

	// Set mtime of dst to LATER
	timeB := timeA.Add(time.Second * 1)
	require.NoError(t, os.Chtimes(dst, timeB, timeB))

	// try B, should not re-download, because newer
	require.NoError(t, toCustomPath(ts.URL, dst))
	timeC := getMtime(dst)
	require.Equal(t, timeB, timeC, "2nd download should not have touched mtime")
}

func TestReplaceSelf(t *testing.T) {
	self, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, toCustomPath(ts.URL, self))
}

func TestGetCachePath(t *testing.T) {
	// Save original XDG paths to restore later
	origCacheHome := xdg.CacheHome
	defer func() {
		xdg.CacheHome = origCacheHome
		xdg.Reload()
	}()

	tmpDir, err := os.MkdirTemp("", "urlfilecache-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name      string
		setupFunc func() // Setup the test environment
		wantErr   bool
		wantDir   string // Expected directory prefix for cache path
	}{
		{
			name: "normal case - cache dir exists and is writable",
			setupFunc: func() {
				cacheDir := filepath.Join(tmpDir, "normal", ".cache")
				require.NoError(t, os.MkdirAll(cacheDir, 0o755))
				xdg.CacheHome = cacheDir
			},
			wantErr: false,
			wantDir: filepath.Join(tmpDir, "normal", ".cache"),
		},
		{
			name: "cache dir doesn't exist - should create it",
			setupFunc: func() {
				xdg.CacheHome = filepath.Join(tmpDir, "missing", ".cache")
			},
			wantErr: false,
			wantDir: filepath.Join(tmpDir, "missing", ".cache"),
		},
		{
			name: "cache dir not writable - should fallback to other locations",
			setupFunc: func() {
				xdg.CacheHome = filepath.Join("/", "readonly", ".cache")
			},
			wantErr: false,
			wantDir: "/tmp",
		},
	}

	testURL := "https://example.com/test.txt"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setupFunc()

			got, err := getCachePath(testURL, "testdata", &options{})
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotEmpty(t, got)

			// Try to create the directory structure
			dir := filepath.Dir(got)
			require.Equal(t, filepath.Join(tt.wantDir, "urlfilecache.test"), dir)

			err = os.MkdirAll(dir, 0o755)
			require.NoError(t, err, "Should be able to create cache directory structure")

			// Try to create a test file
			f, err := os.Create(got)
			require.NoError(t, err, "Should be able to create file in cache path")
			f.Close()
		})
	}
}

func TestEmptyLastModified(t *testing.T) {
	ts, err := http.ParseTime("")
	require.Error(t, err)
	require.Equal(t, time.Time{}, ts)
}

func TestEmptyLastModifiedHeader(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately not setting Last-Modified header
		_, _ = w.Write([]byte("test content"))
	}))
	defer ts.Close()

	f, err := os.CreateTemp("", "urlfilecache")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	err = toCustomPath(ts.URL, f.Name())
	require.NoError(t, err)
}

func TestETag(t *testing.T) {
	const testETag = `"123456789"`
	etagServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ifNoneMatch := r.Header.Get("If-None-Match"); ifNoneMatch == testETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", testETag)
		_, _ = w.Write([]byte("test content"))
	}))
	defer etagServer.Close()

	// First request should save the ETag
	f, err := os.CreateTemp("", "urlfilecache")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	require.NoError(t, toCustomPath(etagServer.URL, f.Name()))

	// Verify ETag was saved
	etagPath, err := getCachePath(etagServer.URL, etagSuffix, &options{})
	require.NoError(t, err)
	defer os.Remove(etagPath)

	etag, err := readFile(etagServer.URL, etagSuffix, &options{})
	require.NoError(t, err)
	require.Equal(t, testETag, etag)

	// Second request should use ETag and get 304
	timeBeforeSecondRequest := getMtime(f.Name())
	require.NoError(t, toCustomPath(etagServer.URL, f.Name()))
	timeAfterSecondRequest := getMtime(f.Name())

	// File should not have been modified since we got a 304
	require.Equal(t, timeBeforeSecondRequest, timeAfterSecondRequest)
}

func TestZeroTimeChtimes(t *testing.T) {
	f, err := os.CreateTemp("", "test")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	require.NoError(t, f.Close())
	require.NoError(t, os.Chtimes(f.Name(), time.Time{}, time.Time{}))
}
