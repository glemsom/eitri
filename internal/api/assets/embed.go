package assets

import (
	"embed"
)

//go:embed htmx.min.js
//go:embed eitri.css
//go:embed eitri-stream.js
//go:embed eitri-composer.js
//go:embed eitri-mermaid.js
var Files embed.FS
