# Simple URL file cache

Simple URL fetcher & cache. Will only fetch updated resource when actually newer (using `If-Modified-Since` and `ETag` HTTP headers), so suitable for large data files. Caching is based on best-effort and could not happen if server doesn't send appropriate headers or local files couldn't be written.

## Basic Usage

```go
import "github.com/gwillem/urlfilecache"

url := "https://google.com/robots.txt"
path, err := urlfilecache.ToPath(url)
// /home/you/.cache/<cmd>/<hash>.data
data, err := os.ReadFile(path)
```

## Options

`ToPath(url string, opts ...Option)` accepts the following options:

- `WithTTL(duration)` - Don't re-fetch if file was modified within TTL period
- `WithPath(path)` - Use a custom cache location instead of auto-generated path
- `WithPackagePath()` - Use calling package name instead of executable name in path. This is useful if multiple applications use the same library and a single download would suffice.

### Example: TTL

```go
// Cache a file with 1 hour TTL
path, err := urlfilecache.ToPath("https://example.com/file.txt",
    urlfilecache.WithTTL(time.Hour))
```

### Example: Custom Path

```go
// Use a specific cache location
path, err := urlfilecache.ToPath("https://example.com/file.txt",
    urlfilecache.WithPath("/tmp/myfile"))
```

### Example: Package Path

```go
// Use package name instead of executable name in cache path
path, err := urlfilecache.ToPath("https://example.com/file.txt",
    urlfilecache.WithPackagePath())
// /home/you/.cache/mypackage/<hash>.data (instead of /home/you/.cache/myapp/<hash>.data)
```

## Upgrading

All public methods have been consolidated under `ToPath` with optional `Option` parameters. If you're upgrading from an older version:

```go
// Old API
ToPathTTL(url, time.Hour)
ToCustomPath(url, "/tmp/file")
ToCustomPathTTL(url, "/tmp/file", time.Hour)

// New API
ToPath(url, WithTTL(time.Hour))
ToPath(url, WithPath("/tmp/file"))
ToPath(url, WithPath("/tmp/file"), WithTTL(time.Hour))
```

## Cache Location

The cache location is determined in this order:

1. `$XDG_CACHE_HOME` (usually `~/.cache`)
2. `/tmp`
3. `/var/tmp`
4. `/dev/shm`

## Cache Behavior

Webservers such as Nginx honour the `If-Modified-Since` header exclusively with an exact timestamp match. To mitigate this, `urlfilecache` will modify the mtime of the downloaded file to match the `Last-Modified` as given by the server.

This means that if the local file is newer than the server copy, an extra download will be triggered. After that, the local & remote timestamps will match so caching is activated.

This may be a problem for development of self-updating binaries, because the newly built local binary is always newer than the server copy. In that case, ensure that your webserver will use caching for anything older than the given timestamp. For nginx, you can add this line to `nginx.conf`:

```
    if_modified_since before;
```
