package testpkg

import "github.com/gwillem/urlfilecache"

// FetchWithPackageName calls ToPath with UsePackageName option from this package
func FetchWithPackageName(url string) (string, error) {
	return urlfilecache.ToPath(url, urlfilecache.UsePackagePath)
}

// FetchDefault calls ToPath without UsePackageName option
func FetchDefault(url string) (string, error) {
	return urlfilecache.ToPath(url)
}
