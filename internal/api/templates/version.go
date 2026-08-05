package templates

import "github.com/glemsom/eitri/internal/api/assets"

// assetVersion is the cache-busting value appended to static asset URLs.
// It is a content hash of the embedded assets, so the URL changes whenever
// the asset content changes — which makes long-lived immutable caching safe
// (see internal/api/server.go, /static/* handler).
func assetVersion() string {
	if assets.CacheBustVersion == "" {
		return "dev"
	}
	return assets.CacheBustVersion
}

// staticAsset returns a cache-busted URL for the given static asset path.
// Assets are served with an immutable Cache-Control, so every reference must
// carry the version query string to invalidate stale copies on release.
func staticAsset(path string) string {
	return path + "?v=" + assetVersion()
}
