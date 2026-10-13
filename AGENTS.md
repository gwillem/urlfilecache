# urlfilecache

- Target mtime = server `Last-Modified` = content version, sent as `If-Modified-Since`. TTL clock = mtime of `<hash>.checked` sidecar. Never touch the target on 304; keeping both in the target mtime broke the TTL (5cfbe8d) and made callers that compare mtimes (go-selfupdate) see an update on every 304 (v0.0.5).
- Target is only changed via rename of a complete, validated temp file from the same dir. go-selfupdate relies on this (`os.SameFile` before/after).
- Write sidecars only after the rename, else a failed download marks the old copy as current.
- Tests set `XDG_CACHE_HOME` to a temp dir in `TestMain`.
