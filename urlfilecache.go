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
				os.Remove(f.Name())
				return candidate, nil
			}
			continue
		}

		// Try to create the directory
		if err := os.MkdirAll(candidate, 0o755); err == nil {
			if f, err := os.CreateTemp(candidate, ".test"); err == nil {
				os.Remove(f.Name())
				return candidate, nil
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

// ToPath will calculate unique cache path based on program name and source URL.
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

	// Get old mtime
	mtime := getMtime(path) // returns empty time if nonexist

	// Don't bother if file was recently written
	if !mtime.IsZero() && opt.ttl > 0 && time.Since(mtime) < opt.ttl {
		Log.Println("TTL", opt.ttl, "not expired for", path)
		return nil
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	// Work around broken ETag handling for compressed responses
	// such as torproject.org
	req.Header.Set("Accept-Encoding", "identity")

	if etag, _ := readFile(url, etagSuffix, opt); etag != "" {
		Log.Printf("Existing ETag: %q", etag)
		req.Header.Set("If-None-Match", etag)
	}

	// Only conditional request when we have an mtime
	if lm, _ := readFile(url, sinceSuffix, opt); lm != "" {
		Log.Printf("Existing last modified: %v", lm)
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

	// happy path
	if resp.StatusCode == http.StatusNotModified {
		// Bump mtime so the TTL suppresses revalidation until it expires again.
		// Without this the TTL stays expired forever and every call hits the network.
		now := time.Now()
		if err := os.Chtimes(path, now, now); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotModified {
		return fmt.Errorf("bad HTTP response %s, should try previous copy instead", resp.Status)
	}

	if err := writeFile(url, etagSuffix, resp.Header.Get("ETag"), opt); err != nil {
		return err
	}

	if err := writeFile(url, sinceSuffix, resp.Header.Get("Last-Modified"), opt); err != nil {
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
