# Simple URL file cache

Simple URL fetcher & cache. Will only fetch updated resource when actually newer (using `If-Modified-Since` HTTP header), so suitable for large data files.

```go
import "github.com/gwillem/urlfilecache"

url := "https://google.com/robots.txt"
fmt.Println(urlfilecache.ToPath(url))
// /home/you/.cache/<yourapp>/<hash>.urlcache
```
