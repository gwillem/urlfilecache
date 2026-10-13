package urlfilecache

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/adrg/xdg"
)

const (
	dataSuffix    = "data"
	etagSuffix    = "etag"
	checkedSuffix = "checked" // mtime is the time of the last server check, for the TTL

	defaultDialTimeout  = 10 * time.Second
	tlsHandshakeTimeout = 10 * time.Second
)

// responseHeaderTimeout limits the wait for response headers once the request
// is sent. A var so tests can shorten it.
var responseHeaderTimeout = 30 * time.Second

// Log receives debug output, discarded by default
var Log = log.New(io.Discard, "urlfilecache", log.LstdFlags)

func getCachePath(url, suffix string, opt *options) (string, error) {
	if opt.cacheDir == "" {
		var err error
		opt.cacheDir, err = resolveCacheDir(filepath.Base(os.Args[0]))
		if err != nil {
			return "", err
		}
	}
	hash := fmt.Sprintf("%x", sha1.Sum([]byte(url)))
	return filepath.Join(opt.cacheDir, hash+"."+suffix), nil
}

// resolveCacheDir finds a writable cache directory for the given app name.
func resolveCacheDir(name string) (string, error) {
	locations := []string{
		xdg.CacheHome,
		"/tmp",
		"/var/tmp",
		"/dev/shm",
	}

	for _, dir := range locations {
		candidate := filepath.Join(dir, name)

		// Check if directory already exists
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			if f, err := os.CreateTemp(candidate, ".test"); err == nil {
				os.Remove(f.Name()) //nolint:errcheck
				return candidate, nil
			}
			continue
		}

		// Try to create the directory
		if err := os.MkdirAll(candidate, 0o755); err == nil {
			if f, err := os.CreateTemp(candidate, ".test"); err == nil {
				os.Remove(f.Name()) //nolint:errcheck
				return candidate, nil
			}
		}
	}

	return "", fmt.Errorf("no writable location found in: %v", locations)
}

// newClient returns a client that gives up on unreachable or unresponsive
// servers, but does not limit the duration of the body download.
func newClient(dialTimeout time.Duration) *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}}
}

// ToPath downloads url to a cache path derived from the program name and url
// (or to the WithPath target) and returns that path. An existing copy is
// revalidated with If-Modified-Since, taken from its mtime, and If-None-Match.
//
// The target is only changed by renaming a complete download over it, which
// gets the server's Last-Modified as mtime. A 304 leaves the target untouched.
func ToPath(url string, opts ...Option) (string, error) {
	opt := &options{}
	for _, o := range opts {
		o(opt)
	}

	// Resolve cache directory once, so data/etag/since files all use the
	// same directory regardless of internal call depth.
	if opt.cacheDir == "" {
		var name string
		if opt.usePackageName {
			// skip 2: getPackageName -> ToPath -> caller
			name = getPackageName(2)
		} else {
			name = filepath.Base(os.Args[0])
		}
		var err error
		opt.cacheDir, err = resolveCacheDir(name)
		if err != nil {
			return "", err
		}
	}

	if opt.path == "" {
		var err error
		opt.path, err = getCachePath(url, dataSuffix, opt)
		if err != nil {
			return "", err
		}
	}

	if err := fetch(url, opt.path, opt); err != nil {
		return "", err
	}
	return opt.path, nil
}

func fetch(url, path string, opt *options) error {
	Log.Println("Request for", url, "using", path)

	// Ensure parent dir
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}

	checkedPath, err := getCachePath(url, checkedSuffix, opt)
	if err != nil {
		return err
	}

	// Existing copy, nil if there is none
	old, err := os.Stat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	// Don't bother if the server was asked recently
	if old != nil && opt.ttl > 0 && recentlyChecked(checkedPath, opt.ttl) {
		Log.Println("TTL", opt.ttl, "not expired for", path)
		return nil
	}

	// Create the temp file before the request, so an unwritable dir fails
	// without a download. Unique name, so concurrent fetches don't collide.
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // fails after a successful rename
	defer tmp.Close()           //nolint:errcheck // already closed on success

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	// Work around broken ETag handling for compressed responses
	// such as torproject.org
	req.Header.Set("Accept-Encoding", "identity")

	// Conditional request only when there is a copy to fall back on
	if old != nil {
		if etag, _ := readFile(url, etagSuffix, opt); etag != "" {
			Log.Printf("Existing ETag: %q", etag)
			req.Header.Set("If-None-Match", etag)
		}
		req.Header.Set("If-Modified-Since", old.ModTime().UTC().Format(http.TimeFormat))
	}

	/*

			GRRR somehow cloudflare and/or nginx does not respect the if-modified-since header
			AHA there it is: http://nginx.org/en/docs/http/ngx_http_core_module.html#if_modified_since

			corediff on  wdg/vendor [!?] via 🐹 v1.20.2
		❯ curl -sH "If-Modified-Since: Fri, 31 Mar 2023 15:00:59 GMT" -I https://sansec.io/downloads/darwin-arm64/corediff | egrep 'last-modified|HTTP/2'
		HTTP/2 304
		last-modified: Fri, 31 Mar 2023 15:00:59 GMT

		corediff on  wdg/vendor [!?] via 🐹 v1.20.2
		❯ curl -sH "If-Modified-Since: Sat, 01 Apr 2023 22:00:07 GMT" -I https://sansec.io/downloads/darwin-arm64/corediff | egrep 'last-modified|HTTP/2'
		HTTP/2 200
		last-modified: Fri, 31 Mar 2023 15:00:59 GMT

	*/

	dialTimeout := defaultDialTimeout
	if opt.dialTimeout > 0 {
		dialTimeout = opt.dialTimeout
	}
	client := newClient(dialTimeout)
	defer client.CloseIdleConnections()

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	Log.Printf("Response: %d (len: %d)", resp.StatusCode, resp.ContentLength)

	switch {
	case resp.StatusCode == http.StatusNotModified && old != nil:
		return touch(checkedPath)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("bad HTTP response %s, should try previous copy instead", resp.Status)
	}

	lastModified, lmErr := http.ParseTime(resp.Header.Get("Last-Modified"))

	if opt.onlyIfNewer && old != nil {
		if lmErr != nil {
			return fmt.Errorf("no valid Last-Modified from %s, cannot tell if it is newer than %s", url, path)
		}
		if !lastModified.After(old.ModTime()) {
			Log.Println("Server copy from", lastModified, "is not newer than", path)
			return touch(checkedPath)
		}
	}

	bytesWritten, err := io.Copy(tmp, resp.Body)
	if err != nil {
		return err
	}
	Log.Println("Wrote", bytesWritten, "bytes to", tmp.Name())

	if err := tmp.Close(); err != nil {
		return err
	}

	if opt.validator != nil {
		if err := opt.validator(tmp.Name()); err != nil {
			return fmt.Errorf("invalid download from %s: %w", url, err)
		}
	}

	// Set mode (ie execute permissions) and mtime before the rename, so the
	// target never has the wrong ones
	mode := fs.FileMode(0o644)
	if old != nil {
		mode = old.Mode()
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}

	// The mtime is the version of the content, used for If-Modified-Since.
	// Nginx by default only answers 304 on an exact match.
	if lmErr == nil {
		if err := os.Chtimes(tmp.Name(), time.Time{}, lastModified); err != nil {
			return err
		}
	}

	// Atomic replace, also works for a running executable
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}

	// Sidecars only after the content is in place, so a failed download
	// can't mark the old copy as current
	if err := writeFile(url, etagSuffix, resp.Header.Get("ETag"), opt); err != nil {
		return err
	}
	return touch(checkedPath)
}

// recentlyChecked reports whether the server was asked within ttl.
func recentlyChecked(checkedPath string, ttl time.Duration) bool {
	fi, err := os.Stat(checkedPath)
	return err == nil && time.Since(fi.ModTime()) < ttl
}

// touch sets the mtime of path to now, creating it if needed.
func touch(path string) error {
	now := time.Now()
	err := os.Chtimes(path, now, now)
	if errors.Is(err, fs.ErrNotExist) {
		return os.WriteFile(path, nil, 0o600)
	}
	return err
}

func readFile(url, suffix string, opt *options) (string, error) {
	path, err := getCachePath(url, suffix, opt)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	return string(data), err
}

func writeFile(url, suffix, data string, opt *options) error {
	path, err := getCachePath(url, suffix, opt)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), 0o600)
}
