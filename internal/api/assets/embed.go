package assets

import (
	"embed"
)

//go:embed htmx.min.js
//go:embed eitri.css
//go:embed eitri-stream.js
var Files embed.FS
