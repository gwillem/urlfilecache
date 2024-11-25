package urlfilecache

import (
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestURLFileCache(t *testing.T) {
	// url := "https://google.com/robots.txt"
	url := "https://sansec.io/ext/files/test.txt"
	path, err := ToPath(url)
	require.NoError(t, err)
	fmt.Println(path)
}

func TestToCustomPath(t *testing.T) {
	f, err := os.CreateTemp("", "urlfilecache")
	if err != nil {
		log.Fatal(err)
	}
	defer os.Remove(f.Name())

	url := "https://sansec.io/robots.txt"
	dst := f.Name()

	// try A
	require.NoError(t, ToCustomPath(url, dst))

	timeA := getMtime(dst)

	// Set mtime of dst to LATER
	timeB := timeA.Add(time.Second * 1)
	require.NoError(t, os.Chtimes(dst, timeB, timeB))

	// try B, should not re-download, because newer
	require.NoError(t, ToCustomPath(url, dst))
	timeC := getMtime(dst)
	require.Equal(t, timeB, timeC, "2nd download should not have touched mtime")
}

func TestReplaceSelf(t *testing.T) {
	t.SkipNow()
	url := "https://sansec.io/robots.txt"
	self, _ := os.Executable()
	if e := ToCustomPath(url, self); e != nil {
		log.Fatal(e)
	}
}
