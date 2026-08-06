//go:build ignore

// gen_eitri_css.go regenerates the single embedded eitri.css aggregate from
// the per-area CSS partials under partials/. It runs via `go generate` in
// embed.go, or `go run gen_eitri_css.go` from this directory:
//
//	go generate ./internal/api/assets
//
// The concatenation order is read from partials/order.txt so that the build
// order, the lint-guard test (css_partials_test.go) and this generator can
// never drift apart. eitri.css is committed so that the `//go:embed eitri.css`
// directive in embed.go always has a file to embed even before a generate step
// has run.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		fatal("getwd: %v", err)
	}

	orderBytes, err := os.ReadFile(filepath.Join(dir, "partials", "order.txt"))
	if err != nil {
		fatal("read partials/order.txt: %v", err)
	}
	var order []string
	for _, raw := range strings.Split(string(orderBytes), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		order = append(order, line)
	}
	if len(order) == 0 {
		fatal("partials/order.txt lists no partials")
	}

	var out bytes.Buffer
	out.WriteString("/* This file is generated from the CSS partials under partials/ by\n")
	out.WriteString("   `go generate ./internal/api/assets`. Do not edit eitri.css directly —\n")
	out.WriteString("   edit the partials and regenerate. See css_partials_test.go. */\n")
	for _, rel := range order {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			fatal("read %s: %v", rel, err)
		}
		out.Write(data)
		if !bytes.HasSuffix(data, []byte("\n")) {
			out.WriteString("\n")
		}
		out.WriteString("\n")
	}

	dest := filepath.Join(dir, "eitri.css")
	if err := os.WriteFile(dest, out.Bytes(), 0o644); err != nil {
		fatal("write %s: %v", dest, err)
	}
	fmt.Printf("regenerated %s from %d partials\n", dest, len(order))
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "gen_eitri_css: "+format+"\n", args...)
	os.Exit(1)
}
