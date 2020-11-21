package urlfilecache

import (
	"fmt"
	"testing"
)

func TestURLFileCache(t *testing.T) {
	// url := "https://google.com/robots.txt"
	url := "https://sansec.io/ext/files/test.txt"
	path := ToPath(url)
	fmt.Println(path)
}
