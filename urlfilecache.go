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

func getCachePath(url string) string {
	hash := fmt.Sprintf("%x", sha1.Sum([]byte(url)))
	self := filepath.Base(os.Args[0])

	name, err := xdg.CacheFile(fmt.Sprintf("%s/%s.urlcache", self, hash))
	if err != nil {
		log.Fatal(err)
	}
	return name
}

func getMtime(path string) *time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	mtime := fi.ModTime().UTC()
	return &mtime
}

// ToPath will calculate unique cache path based on program name and source URL. Silently ignores errors.
func ToPath(url string) (path string) {
	path = getCachePath(url)
	ToCustomPath(url, path) // ignore err
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
	mtime := getMtime(path) // returns nil if nonexist

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Fatal(err)
	}

	// Only conditional request when we have an mtime
	if mtime != nil {
		// fmt.Println("if modified since:\n" + mtime.Format(http.TimeFormat))
		req.Header.Set("If-Modified-Since", mtime.Format(http.TimeFormat))
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 304 {
		// Urray! No need to fetch newer
		// fmt.Println("Using disk cache because URL is not newer")
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		// fmt.Println(")
		return fmt.Errorf("bad HTTP response %s so trying previous copy instead", resp.Status)
		// log.Fatalf("bad status: %s", resp.Status)
	}

	lastModified, _ := http.ParseTime(resp.Header.Get("Last-Modified"))

	// fmt.Println("last-modified:\n" + resp.Header.Get("Last-Modified"))
	// ts, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	// fmt.Println(ts)
	// fmt.Println(ts.Format(http.TimeFormat))

	fh, err := os.Create(path)
	if err != nil {
		return err
	}

	// Writer the body to file
	_, err = io.Copy(fh, resp.Body)
	fh.Close()
	if err != nil {
		return err
	}

	// Sync mtime for downloaded file with given header
	err = os.Chtimes(path, lastModified, lastModified)
	if err != nil {
		return err
	}

	// if mtime == nil {
	// 	fmt.Println("Downloaded new copy from", url)
	// } else {
	// 	fmt.Println("Replaced existing disk cache with newer copy")
	// }

	// Hurray!
	return nil
}
