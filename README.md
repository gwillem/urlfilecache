# Simple URL file cache

Simple URL fetcher & cache. Will only fetch updated resource when actually newer (using `If-Modified-Since` HTTP header), so suitable for large data files.

```go
import "github.com/gwillem/urlfilecache"

url := "https://google.com/robots.txt"
path := urlfilecache.toPath(url)
// /home/you/.cache/<cmd>/<hash>.urlcache
data, err := os.Readfile(path)
```

## On caching behaviour

Webservers such as Nginx honour the `If-Modified-Since` header exclusively with an exact timestamp match. To mitigate this, `urlfilecache` will modify the mtime of the downloaded file to match the `Last-Modified` as given by the server. 

Relevant for `ToCustomPath`: this means that if the local file is newer than the server copy, an extra download will be triggered. After that, the local & remote timestamps will match so caching is activated. 

This may be a problem for development of self-updating binaries, because the newly built local binary is always newer than the server copy. In that case, ensure that your webserver will use caching for anything older than the given timestamp. For nginx, you can add this line to `nginx.conf`:

```
    if_modified_since before;
```

Alternatively, this library could be rewritten to use HEAD probe requests to discover the remote `Last-Modified` timestamp, and not depend on any server-side caching at all. 