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
	dataSuffix = "data"
	etagSuffix = "etag"
)

var Log = log.New(os.Stderr, "urlfilecache", log.LstdFlags)

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
	// Ensure parent dir
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}

	// Get old mtime
	mtime := getMtime(path) // returns empty time if nonexist

	// Don't bother if file hasn't expired
	if !mtime.IsZero() && time.Since(mtime) < ttl {
		return nil
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	if etag := readETag(url); etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	// Only conditional request when we have an mtime
	if !mtime.IsZero() {
		req.Header.Set("If-Modified-Since", mtime.Format(http.TimeFormat))
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

	if resp.StatusCode == 304 {
		// Urray! No need to fetch newer
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad HTTP response %s so trying previous copy instead", resp.Status)
	}

	if etag := resp.Header.Get("ETag"); etag != "" {
		if err := writeETag(url, etag); err != nil {
			return err
		}
	}

	// dont care if no last-modified header
	lastModified, _ := http.ParseTime(resp.Header.Get("Last-Modified"))

	// Use tmpPath for atomic writes, and to be able to replace /proc/$$/self
	tmpPath := path + ".tmp"

	fh, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	if _, err = io.Copy(fh, resp.Body); err != nil {
		return err
	}

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

	// Sync mtime for downloaded file with given header. This is required
	// because nginx (by default) only uses caching for exact timestamp matches
	if e := os.Chtimes(path, lastModified, lastModified); e != nil {
		return e
	}

	return nil
}

func readETag(url string) string {
	etagPath, err := getCachePath(url, etagSuffix)
	if err != nil {
		return ""
	}
	data, _ := os.ReadFile(etagPath)
	return string(data)
}

func writeETag(url, etag string) error {
	etagPath, err := getCachePath(url, etagSuffix)
	if err != nil {
		return err
	}
	return os.WriteFile(etagPath, []byte(etag), 0o600)
}
