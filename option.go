package urlfilecache

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Option is a functional option for configuring cache behavior
type Option func(*options)

type options struct {
	usePackageName bool
	ttl            time.Duration
	path           string
}

func getPackageName(skip int) string {
	pc, _, _, ok := runtime.Caller(skip)
	if !ok {
		return filepath.Base(os.Args[0])
	}
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return filepath.Base(os.Args[0])
	}
	// Function name format is: "package/path.FuncName" or "package/path.(*Type).Method"
	fullName := fn.Name()
	// Extract package name (last component before the function/method)
	lastSlash := strings.LastIndex(fullName, "/")
	if lastSlash == -1 {
		lastSlash = 0
	} else {
		lastSlash++
	}
	firstDot := strings.Index(fullName[lastSlash:], ".")
	if firstDot == -1 {
		return filepath.Base(os.Args[0])
	}
	return fullName[lastSlash : lastSlash+firstDot]
}

// UsePackagePath configures ToPath to use the package name of the caller
// instead of os.Args[0]
func UsePackagePath(o *options) {
	o.usePackageName = true
}

// WithTTL configures the time-to-live duration for the cache
func WithTTL(ttl time.Duration) Option {
	return func(o *options) {
		o.ttl = ttl
	}
}

// WithPath configures a custom cache path instead of auto-generating one
func WithPath(path string) Option {
	return func(o *options) {
		o.path = path
	}
}
