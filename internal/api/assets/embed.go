package assets

import (
	"embed"
)

// The single embedded stylesheet eitri.css is generated from the per-area CSS
// partials under partials/ (see gen_eitri_css.go and css_partials_test.go).
//
//go:generate go run gen_eitri_css.go

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
//go:embed eitri-session-id.js
//go:embed eitri-stream.js
//go:embed eitri-stream-common.js
//go:embed eitri-stream-toolcards.js
//go:embed eitri-stream-announcer.js
//go:embed eitri-stream-tokens.js
//go:embed eitri-stream-confirmation.js
//go:embed eitri-stream-scroll.js
//go:embed eitri-stream-render.js
//go:embed eitri-composer.js
//go:embed eitri-renderers.js
//go:embed eitri-mermaid.js
//go:embed eitri-lazy-load.js
//go:embed eitri-persona-selector.js
//go:embed eitri-session-rename.js
//go:embed eitri-settings.js
//go:embed eitri-context.js
//go:embed eitri-resize.js
//go:embed eitri-events.js
//go:embed face.webp
//go:embed favicon-32.png
//go:embed favicon-16.png
//go:embed pwa-icon-192.png
//go:embed pwa-icon-512.png
//go:embed manifest.json
//go:embed sw.js
var Files embed.FS

// CSSPartials exposes the per-area CSS partial files for the lint-guard test
// (css_partials_test.go) so it can verify the generated aggregate eitri.css is
// in sync with its sources without relying on the working directory. These are
// deliberately NOT part of Files: only the assembled eitri.css is served, so
// /static/ never exposes the partials.
//
//go:embed partials
var CSSPartials embed.FS
