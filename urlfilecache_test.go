package urlfilecache

import (
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	// Keep sidecar files out of the user's cache dir
	cacheHome, err := os.MkdirTemp("", "urlfilecache-cache-*")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.Setenv("XDG_CACHE_HOME", cacheHome); err != nil {
		log.Fatal(err)
	}
	xdg.Reload()

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
	os.RemoveAll(cacheHome) //nolint:errcheck

	os.Exit(code)
}

func TestURLFileCache(t *testing.T) {
	path, err := ToPath(ts.URL)
	defer os.Remove(path) //nolint:errcheck
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
	defer os.Remove(f.Name()) //nolint:errcheck
	dst := f.Name()

	// try A
	require.NoError(t, toCustomPath(ts.URL, dst))

	// Overwrite with a sentinel, so a re-download would be visible in the content.
	require.NoError(t, os.WriteFile(dst, []byte("sentinel"), 0o644))

	// try B, server answers 304, so the body must be left alone
	require.NoError(t, toCustomPath(ts.URL, dst))
	content, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, "sentinel", string(content), "2nd request should not have re-downloaded")
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
	defer os.RemoveAll(tmpDir) //nolint:errcheck

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
				// Below a regular file, so even root can't create it
				file := filepath.Join(tmpDir, "file")
				require.NoError(t, os.WriteFile(file, nil, 0o644))
				xdg.CacheHome = filepath.Join(file, ".cache")
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
			f.Close() //nolint:errcheck
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
	defer os.Remove(f.Name()) //nolint:errcheck

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
	defer os.Remove(f.Name()) //nolint:errcheck

	require.NoError(t, toCustomPath(etagServer.URL, f.Name()))

	// Verify ETag was saved
	etagPath, err := getCachePath(etagServer.URL, etagSuffix, &options{})
	require.NoError(t, err)
	defer os.Remove(etagPath) //nolint:errcheck

	etag, err := readFile(etagServer.URL, etagSuffix, &options{})
	require.NoError(t, err)
	require.Equal(t, testETag, etag)

	// Second request should use ETag and get 304
	require.NoError(t, os.WriteFile(f.Name(), []byte("sentinel"), 0o644))
	require.NoError(t, toCustomPath(etagServer.URL, f.Name()))

	// Content should be untouched since we got a 304
	content, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	require.Equal(t, "sentinel", string(content))
}

func TestZeroTimeChtimes(t *testing.T) {
	f, err := os.CreateTemp("", "test")
	require.NoError(t, err)
	defer os.Remove(f.Name()) //nolint:errcheck
	require.NoError(t, f.Close())
	require.NoError(t, os.Chtimes(f.Name(), time.Time{}, time.Time{}))
}

func TestTTLSuppressesRequestsUntilExpired(t *testing.T) {
	srv, requests := contentServer(t, lastMod, "content")
	path := filepath.Join(t.TempDir(), "target")

	require.NoError(t, toCustomPath(srv.URL, path, WithTTL(time.Hour)))
	require.NoError(t, toCustomPath(srv.URL, path, WithTTL(time.Hour)))
	require.EqualValues(t, 1, requests.Load(), "fresh TTL should suppress the request")

	// Expire the TTL, so the next call revalidates and gets a 304
	checked, err := getCachePath(srv.URL, checkedSuffix, &options{})
	require.NoError(t, err)
	expired := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(checked, expired, expired))

	require.NoError(t, toCustomPath(srv.URL, path, WithTTL(time.Hour)))
	require.EqualValues(t, 2, requests.Load())

	// The 304 restarted the TTL
	require.NoError(t, toCustomPath(srv.URL, path, WithTTL(time.Hour)))
	require.EqualValues(t, 2, requests.Load(), "TTL should suppress the request after a 304")
}

var (
	lastMod = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	older   = lastMod.Add(-24 * time.Hour)
	newer   = lastMod.Add(24 * time.Hour)
)

// newTarget writes content to a fresh file with the given mtime.
func newTarget(t *testing.T, content string, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "target")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	require.NoError(t, os.Chtimes(path, mtime, mtime))
	return path
}

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// contentServer serves body with Last-Modified lm and honours conditional
// headers like a regular webserver. It counts requests.
func contentServer(t *testing.T, lm time.Time, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		http.ServeContent(w, r, "", lm, strings.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// truncatingServer answers the first request with Last-Modified lm, an ETag
// and a body shorter than its Content-Length, then behaves like contentServer.
func truncatingServer(t *testing.T, lm time.Time, body string) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		if n.Add(1) == 1 {
			w.Header().Set("Last-Modified", lm.Format(http.TimeFormat))
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("partial"))
			return
		}
		http.ServeContent(w, r, "", lm, strings.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTruncatedDownloadDoesNotPoisonCache(t *testing.T) {
	srv := truncatingServer(t, lastMod, "complete")
	path := newTarget(t, "old", older)

	require.Error(t, toCustomPath(srv.URL, path))
	require.Equal(t, "old", readString(t, path))

	require.NoError(t, toCustomPath(srv.URL, path))
	require.Equal(t, "complete", readString(t, path), "a failed download must not mark the old copy as current")
}

func TestFailedDownloadLeavesNoTempFile(t *testing.T) {
	srv := truncatingServer(t, lastMod, "complete")
	path := newTarget(t, "old", older)

	require.Error(t, toCustomPath(srv.URL, path))
	require.Equal(t, []string{"target"}, dirEntries(t, filepath.Dir(path)))
}

func TestNotModifiedLeavesTargetUntouched(t *testing.T) {
	srv, _ := contentServer(t, lastMod, "content")
	path := filepath.Join(t.TempDir(), "target")

	require.NoError(t, toCustomPath(srv.URL, path))
	before, err := os.Stat(path)
	require.NoError(t, err)

	require.NoError(t, toCustomPath(srv.URL, path)) // 304
	after, err := os.Stat(path)
	require.NoError(t, err)

	require.True(t, os.SameFile(before, after), "304 must not replace the target")
	require.Equal(t, before.ModTime(), after.ModTime(), "304 must not touch the target mtime")
}

func TestDownloadSetsMtimeToLastModified(t *testing.T) {
	srv, _ := contentServer(t, lastMod, "content")
	path := filepath.Join(t.TempDir(), "target")

	require.NoError(t, toCustomPath(srv.URL, path))
	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, lastMod.Equal(fi.ModTime()), "mtime %v, want %v", fi.ModTime(), lastMod)
}

func TestDownloadReplacesTargetWithNewFile(t *testing.T) {
	srv, _ := contentServer(t, lastMod, "new")
	path := newTarget(t, "old", older)
	before, err := os.Stat(path)
	require.NoError(t, err)

	require.NoError(t, toCustomPath(srv.URL, path))
	after, err := os.Stat(path)
	require.NoError(t, err)

	require.Equal(t, "new", readString(t, path))
	require.False(t, os.SameFile(before, after), "target must be replaced by rename, not rewritten in place")
}

func TestConditionalRequestUsesTargetMtime(t *testing.T) {
	var ims atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ims.Store(r.Header.Get("If-Modified-Since"))
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()
	path := newTarget(t, "local", newer)

	require.NoError(t, toCustomPath(srv.URL, path))
	require.Equal(t, "Tue, 02 Jan 2024 00:00:00 GMT", ims.Load())
}

func TestMissingTargetIsDownloaded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "", lastMod, strings.NewReader("content"))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "target")

	require.NoError(t, toCustomPath(srv.URL, path))
	require.NoError(t, os.Remove(path))

	require.NoError(t, toCustomPath(srv.URL, path))
	require.Equal(t, "content", readString(t, path))
}

func TestUnwritableDirFailsBeforeRequest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	srv, requests := contentServer(t, lastMod, "new")
	path := newTarget(t, "old", older)
	dir := filepath.Dir(path)
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { os.Chmod(dir, 0o755) }) //nolint:errcheck

	err := toCustomPath(srv.URL, path)
	require.ErrorIs(t, err, fs.ErrPermission)
	require.Zero(t, requests.Load(), "should not download what cannot be written")
}

func TestExistingTmpFileIsNotClobbered(t *testing.T) {
	srv, _ := contentServer(t, lastMod, "new")
	path := newTarget(t, "old", older)
	require.NoError(t, os.WriteFile(path+".tmp", []byte("unrelated"), 0o644))

	require.NoError(t, toCustomPath(srv.URL, path))
	require.Equal(t, "unrelated", readString(t, path+".tmp"))
}

func TestReplacePreservesMode(t *testing.T) {
	srv, _ := contentServer(t, lastMod, "new")
	path := newTarget(t, "old", older)
	require.NoError(t, os.Chmod(path, 0o751))

	require.NoError(t, toCustomPath(srv.URL, path))
	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o751), fi.Mode().Perm())
}

func TestNewFileIsWorldReadable(t *testing.T) {
	srv, _ := contentServer(t, lastMod, "new")
	path := filepath.Join(t.TempDir(), "target")

	require.NoError(t, toCustomPath(srv.URL, path))
	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o644), fi.Mode().Perm())
}

func TestUnresponsiveServerTimesOut(t *testing.T) {
	orig := responseHeaderTimeout
	responseHeaderTimeout = 100 * time.Millisecond
	t.Cleanup(func() { responseHeaderTimeout = orig })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	start := time.Now()
	require.Error(t, toCustomPath(srv.URL, filepath.Join(t.TempDir(), "target")))
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestWithDialTimeout(t *testing.T) {
	start := time.Now()
	err := toCustomPath("http://192.0.2.1:81/", filepath.Join(t.TempDir(), "target"),
		WithDialTimeout(200*time.Millisecond))

	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Skipf("192.0.2.1 is not blackholed on this network: %v", err)
	}
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestValidatorRejectsDownload(t *testing.T) {
	srv, _ := contentServer(t, lastMod, "new")
	path := newTarget(t, "old", older)
	errInvalid := errors.New("invalid")

	err := toCustomPath(srv.URL, path, WithValidator(func(string) error { return errInvalid }))
	require.ErrorIs(t, err, errInvalid)
	require.Equal(t, "old", readString(t, path))
	require.Equal(t, []string{"target"}, dirEntries(t, filepath.Dir(path)))
}

func TestValidatorSeesDownloadedContent(t *testing.T) {
	srv, _ := contentServer(t, lastMod, "new")
	path := newTarget(t, "old", older)

	var seen string
	require.NoError(t, toCustomPath(srv.URL, path, WithValidator(func(p string) error {
		seen = readString(t, p)
		return nil
	})))
	require.Equal(t, "new", seen)
	require.Equal(t, "new", readString(t, path))
}

// ignoringServer answers every request with 200, like nginx's default
// "if_modified_since exact" when the dates differ.
func ignoringServer(t *testing.T, lm time.Time, bodies ...string) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := min(int(n.Add(1))-1, len(bodies)-1)
		if !lm.IsZero() {
			w.Header().Set("Last-Modified", lm.Format(http.TimeFormat))
		}
		_, _ = w.Write([]byte(bodies[i]))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOnlyIfNewerKeepsNewerTarget(t *testing.T) {
	srv := ignoringServer(t, lastMod, "server")
	path := newTarget(t, "local", newer)

	require.NoError(t, toCustomPath(srv.URL, path, OnlyIfNewer))
	require.Equal(t, "local", readString(t, path))
}

func TestOnlyIfNewerReplacesOlderTarget(t *testing.T) {
	srv := ignoringServer(t, lastMod, "server")
	path := newTarget(t, "local", older)

	require.NoError(t, toCustomPath(srv.URL, path, OnlyIfNewer))
	require.Equal(t, "server", readString(t, path))
}

func TestOnlyIfNewerSkipsSameLastModified(t *testing.T) {
	srv := ignoringServer(t, lastMod, "v1", "v2")
	path := filepath.Join(t.TempDir(), "target")

	require.NoError(t, toCustomPath(srv.URL, path, OnlyIfNewer))
	require.NoError(t, toCustomPath(srv.URL, path, OnlyIfNewer))
	require.Equal(t, "v1", readString(t, path))
}

func TestOnlyIfNewerRequiresLastModified(t *testing.T) {
	srv := ignoringServer(t, time.Time{}, "server")
	path := newTarget(t, "local", older)

	require.Error(t, toCustomPath(srv.URL, path, OnlyIfNewer))
	require.Equal(t, "local", readString(t, path))
}

func TestOnlyIfNewerDownloadsMissingTarget(t *testing.T) {
	srv := ignoringServer(t, time.Time{}, "server")
	path := filepath.Join(t.TempDir(), "target")

	require.NoError(t, toCustomPath(srv.URL, path, OnlyIfNewer))
	require.Equal(t, "server", readString(t, path))
}

func TestNotModifiedWithoutTargetIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	require.Error(t, toCustomPath(srv.URL, filepath.Join(t.TempDir(), "target")))
}
