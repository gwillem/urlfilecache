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

var Log = log.New(os.Stderr, "urlfilecache", log.LstdFlags)

func getCachePath(url string) string {
	hash := fmt.Sprintf("%x", sha1.Sum([]byte(url)))
	self := filepath.Base(os.Args[0])

	name, err := xdg.CacheFile(fmt.Sprintf("%s/%s.urlcache", self, hash))
	if err != nil {
		Log.Fatal(err)
	}
	return name
}

func getMtime(path string) time.Time {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime().UTC()
	}
	return time.Time{}
}

// ToPath will calculate unique cache path based on program name and source URL. Silently ignores errors.
func ToPath(url string) (path string) {
	path = getCachePath(url)
	if e := ToCustomPath(url, path); e != nil {
		Log.Println("ToCustomPath:", e)
	}
	return path
}

// ToCustomPath uses path as explicit cache location.
func ToCustomPath(url, path string) error {

	// Ensure parent dir
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}

	// Get old mtime
	mtime := getMtime(path) // returns empty time if nonexist

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	// Only conditional request when we have an mtime
	if !mtime.IsZero() {
		// fmt.Println("if modified since:\n" + mtime.Format(http.TimeFormat))

		fmt.Println("setting if-modified-since:", mtime.Format(http.TimeFormat))
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

	if e := fh.Close(); e != nil {
		return e
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
