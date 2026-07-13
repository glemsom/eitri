package assets

import (
	"embed"
)

//go:embed htmx.min.js
//go:embed eitri.css
var Files embed.FS
