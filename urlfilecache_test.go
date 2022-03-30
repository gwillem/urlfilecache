package urlfilecache

import (
	"fmt"
	"log"
	"os"
	"testing"
)

func TestURLFileCache(t *testing.T) {
	// url := "https://google.com/robots.txt"
	url := "https://sansec.io/ext/files/test.txt"
	path := ToPath(url)
	fmt.Println(path)
}

func TestToCustomPath(t *testing.T) {
	url := "https://sansec.io/robots.txt"
	dst := "/tmp/robots.txt"
	ToCustomPath(url, dst)
	fmt.Println(dst)
}

func TestReplaceSelf(t *testing.T) {
	url := "https://sansec.io/robots.txt"
	self, _ := os.Executable()
	if e := ToCustomPath(url, self); e != nil {
		log.Fatal(e)
	}
}
