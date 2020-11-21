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

func ToPath(url string) (path string) {

	path = getCachePath(url)
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
		return path
	}

	if resp.StatusCode != http.StatusOK {
		// fmt.Println("Bad HTTP response", resp.Status, "so trying previous copy instead...")
		return path
		// log.Fatalf("bad status: %s", resp.Status)
	}

	lastModified, _ := http.ParseTime(resp.Header.Get("Last-Modified"))

	// fmt.Println("last-modified:\n" + resp.Header.Get("Last-Modified"))
	// ts, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	// fmt.Println(ts)
	// fmt.Println(ts.Format(http.TimeFormat))

	fh, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}

	// Writer the body to file
	_, err = io.Copy(fh, resp.Body)
	fh.Close()
	if err != nil {
		log.Fatal(err)
	}

	// Sync mtime for downloaded file with given header
	err = os.Chtimes(path, lastModified, lastModified)
	if err != nil {
		log.Fatal(err)
	}

	if mtime == nil {
		// fmt.Println("Downloaded new copy from", url)
	} else {
		// fmt.Println("Replaced existing disk cache with newer copy")
	}

	return path
}
