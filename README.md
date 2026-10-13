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

- `WithTTL(duration)` - Don't ask the server again within the TTL period
- `WithPath(path)` - Use a custom cache location instead of auto-generated path
- `UsePackagePath` - Use calling package name instead of executable name in path. This is useful if multiple applications use the same library and a single download would suffice.
- `WithDialTimeout(duration)` - Give up connecting after this long (default 10s)
- `WithValidator(func(path string) error)` - Check the download before it replaces the existing file. An error discards the download and keeps the old file.
- `OnlyIfNewer` - Keep an existing file unless the server's `Last-Modified` is newer than its mtime, also when the server ignores `If-Modified-Since`. Without `Last-Modified`, this is an error.

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
    urlfilecache.UsePackagePath)
// /home/you/.cache/mypackage/<hash>.data (instead of /home/you/.cache/myapp/<hash>.data)
```

### Example: Validated download

```go
// Only replace with a newer copy, and reject anything that isn't JSON
path, err := urlfilecache.ToPath("https://example.com/data.json",
    urlfilecache.WithPath("/var/lib/app/data.json"),
    urlfilecache.WithDialTimeout(3*time.Second),
    urlfilecache.WithValidator(isJSON),
    urlfilecache.OnlyIfNewer)
```

## Upgrading

### v0.1.0

- The mtime of a cached file is now the server's `Last-Modified`, and the TTL clock moved to a `<hash>.checked` sidecar. Existing `<hash>.since` files are no longer used. Servers with exact-match `If-Modified-Since` (nginx default) send each cached file once more after the upgrade.
- Requests no longer go through `http.DefaultClient`, so changes to it have no effect. See [Timeouts](#timeouts).

### v0.0.2

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

The mtime of a downloaded file is set to the server's `Last-Modified`. The next request sends that mtime as `If-Modified-Since`, plus the stored `ETag` as `If-None-Match`. Conditional headers are only sent when the file exists. An existing file with a mtime but no download history (such as a `WithPath` target that was installed separately) is revalidated against its own mtime.

The file is only changed by renaming a complete download over it, after the optional validator has accepted it. A 304, a failed download or a rejected download leaves it untouched. Files written for the first time get mode 0644; replaced files keep their mode.

Per URL, the cache dir holds two small sidecar files: `<hash>.etag` with the last `ETag` and `<hash>.checked`, whose mtime is the time of the last server check and drives the TTL.

Webservers such as Nginx honour `If-Modified-Since` by default only on an exact timestamp match. A local file that is newer than the server copy (for example a fresh development build of a self-updating binary) then gets a 200 instead of a 304. Use `OnlyIfNewer` to keep such a file, or configure the webserver to answer 304 for anything not older than the given timestamp. For nginx, add this line to `nginx.conf`:

```
    if_modified_since before;
```

## Timeouts

Connecting gives up after 10s (see `WithDialTimeout`), the TLS handshake after 10s and waiting for response headers after 30s. The body download itself is not limited, so large files on slow links still complete.
