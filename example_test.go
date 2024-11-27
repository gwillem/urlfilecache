package urlfilecache

import (
	"fmt"
	"log"
	"os"
	"time"
)

func Example() {
	// Cache a file with 1 hour TTL
	path, err := ToPathTTL("https://example.com/file.txt", time.Hour)
	if err != nil {
		log.Fatal(err)
	}

	// Read the cached file
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Cached file size: %d bytes\n", len(data))
}
