package assets

import (
	"embed"
)

//go:embed htmx.min.js
//go:embed prism-core.min.js
//go:embed prism-go.min.js
//go:embed prism.min.css
//go:embed katex.min.js
//go:embed katex-auto-render.min.js
//go:embed katex.min.css
//go:embed mermaid.min.js
//go:embed fonts/*
//go:embed eitri.css
//go:embed eitri-stream.js
//go:embed eitri-composer.js
//go:embed eitri-renderers.js
//go:embed eitri-mermaid.js
var Files embed.FS
