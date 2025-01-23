package urlfilecache

import (
	"crypto/sha1"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/adrg/xdg"
)

const (
	dataSuffix  = "data"
	etagSuffix  = "etag"
	sinceSuffix = "since"
)

var Log = log.New(io.Discard, "urlfilecache", log.LstdFlags)

func getCachePath(url, suffix string) (string, error) {
	hash := fmt.Sprintf("%x", sha1.Sum([]byte(url)))
	self := filepath.Base(os.Args[0])
	relPath := fmt.Sprintf("%s/%s.%s", self, hash, suffix)

	// Try each location in order until we find one that's writable
	locations := []string{
		xdg.CacheHome,
		"/tmp",
		"/var/tmp",
		"/dev/shm",
	}

	for _, dir := range locations {
		path := filepath.Join(dir, relPath)
		// if path exists and is a regular file (not dir or symlink), it was written before
		if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
			// Try opening in write mode to verify writability
			if f, err := os.OpenFile(path, os.O_WRONLY, 0o644); err == nil {
				f.Close()
				return path, nil
			}
			continue // prev cache file exists but is not writable, find other dir
		}

		// Ensure parent directory exists and is writable
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			// Try creating a temp file to verify writability
			if f, err := os.CreateTemp(filepath.Dir(path), ".test"); err == nil {
				os.Remove(f.Name())
				return path, nil
			}
		}
	}

	return "", fmt.Errorf("no writable location found in: %v", locations)
}

func getMtime(path string) time.Time {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime().UTC()
	}
	return time.Time{}
}

// ToPath will calculate unique cache path based on program name and source URL. Silently ignores errors.
func ToPath(url string) (path string, err error) {
	return ToPathTTL(url, 0)
}

// ToPathTTL will calculate unique cache path based on program name and source URL.
func ToPathTTL(url string, ttl time.Duration) (path string, err error) {
	path, err = getCachePath(url, dataSuffix)
	if err != nil {
		return "", err
	}
	if err := ToCustomPathTTL(url, path, ttl); err != nil {
		return "", err
	}
	return path, nil
}

func ToCustomPath(url, path string) error {
	return ToCustomPathTTL(url, path, 0)
}

// ToCustomPath uses path as explicit cache location.
func ToCustomPathTTL(url, path string, ttl time.Duration) error {
	Log.Println("Request for", url, "using", path)

	// Ensure parent dir
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}

	// Get old mtime
	mtime := getMtime(path) // returns empty time if nonexist

	// Don't bother if file was recently written
	if !mtime.IsZero() && time.Since(mtime) < ttl {
		Log.Println("TTL", ttl, "not expired for", path)
		return nil
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	// Work around broken ETag handling for compressed responses
	// such as torproject.org
	req.Header.Set("Accept-Encoding", "identity")

	if etag, err := readFile(url, etagSuffix); err == nil && etag != "" {
		Log.Printf("Existing ETag: %q", etag)
		req.Header.Set("If-None-Match", etag)
	}

	// Only conditional request when we have an mtime
	if lm, _ := readFile(url, sinceSuffix); lm != "" {
		Log.Printf("Existing file mtime: %v", lm)
		req.Header.Set("If-Modified-Since", lm)
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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	Log.Printf("Response: %d (len: %d)", resp.StatusCode, resp.ContentLength)

	if resp.StatusCode == 304 {
		// happy path
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad HTTP response %s so trying previous copy instead", resp.Status)
	}

	if etag := resp.Header.Get("ETag"); etag != "" {
		if err := writeFile(url, etagSuffix, etag); err != nil {
			return err
		}
	}

	// Write last-modified header to file
	if err := writeFile(url, sinceSuffix, resp.Header.Get("Last-Modified")); err != nil {
		return err
	}

	// Use tmpPath for atomic writes, and to be able to replace /proc/$$/self
	tmpPath := path + ".tmp"

	fh, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	bytesWritten, err := io.Copy(fh, resp.Body)
	if err != nil {
		return err
	}
	Log.Println("Wrote", bytesWritten, "bytes to", tmpPath)

	if err := fh.Close(); err != nil {
		return err
	}

	// Preserve old stat (ie execute permissions),
	// ignore error (file does not exist)
	oldStat, _ := os.Stat(path)

	// Replaces any existing path (if file)
	if e := os.Rename(tmpPath, path); e != nil {
		return e
	}

	// Preserve permissions, if any
	if oldStat != nil {
		if e := os.Chmod(path, oldStat.Mode()); e != nil {
			return e
		}
	}

	return nil
}

func readFile(url, suffix string) (string, error) {
	path, err := getCachePath(url, suffix)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	return string(data), err
}

func writeFile(url, suffix, data string) error {
	path, err := getCachePath(url, suffix)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), 0o600)
}
