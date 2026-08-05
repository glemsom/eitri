package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
)

// CacheBustVersion is a content-derived fingerprint of every file in the
// embedded filesystem. It is stable within a single build and changes
// whenever any asset changes, which makes it a safe cache-busting component
// for static URLs that are served with long-lived immutable caching.
var CacheBustVersion = computeCacheBustVersion()

func computeCacheBustVersion() string {
	h := sha256.New()
	// WalkDir visits entries in lexical order, so the hash is deterministic
	// for a given set of embedded files.
	_ = fs.WalkDir(Files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, readErr := Files.ReadFile(path)
		if readErr != nil {
			return nil
		}
		h.Write([]byte(path))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)[:8]) // 16 hex chars
}
