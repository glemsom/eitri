package assets

import (
	"io"
	"strconv"
	"strings"
	"testing"
)

// TestEmbeddedCSSFromPartials verifies the single embedded eitri.css is exactly
// the concatenation of the per-area CSS partials (in the order listed in
// partials/order.txt). This is the drift guard for the generator
// (gen_eitri_css.go): editing a partial without regenerating eitri.css (or
// editing eitri.css directly) fails here. (issue #1115)
func TestEmbeddedCSSFromPartials(t *testing.T) {
	// Read the partials in the canonical concatenation order.
	orderData, err := CSSPartials.Open("partials/order.txt")
	if err != nil {
		t.Fatalf("open partials/order.txt: %v", err)
	}
	defer orderData.Close()
	orderBytes, _ := io.ReadAll(orderData)
	order := nonEmptyLines(string(orderBytes))

	var joined strings.Builder
	for _, rel := range order {
		f, err := CSSPartials.Open(rel)
		if err != nil {
			t.Errorf("open partial %s: %v", rel, err)
			continue
		}
		b, _ := io.ReadAll(f)
		f.Close()
		joined.Write(b)
		joined.WriteString("\n")
	}
	if t.Failed() {
		t.Fatal("could not read all partials listed in order.txt")
	}

	agg, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer agg.Close()
	aggBytes, _ := io.ReadAll(agg)

	if diff := compareAggregate(aggBytes, joined.String()); diff != "" {
		t.Fatalf("embedded eitri.css is out of sync with partials — run `go generate ./internal/api/assets`:\n%s", diff)
	}
	t.Logf("embedded eitri.css matches concatenation of %d partials", len(order))
}

// TestCSSPartialsSizedUnderLineLimit keeps each partial small enough to
// navigate comfortably. (issue #1115)
func TestCSSPartialsSizedUnderLineLimit(t *testing.T) {
	var paths []string
	collectPartialPaths("partials", &paths)
	const limit = 500
	for _, rel := range paths {
		if strings.HasSuffix(rel, "order.txt") {
			continue
		}
		f, err := CSSPartials.Open(rel)
		if err != nil {
			t.Errorf("open %s: %v", rel, err)
			continue
		}
		b, _ := io.ReadAll(f)
		f.Close()
		n := strings.Count(string(b), "\n") + 1
		if n > limit {
			t.Errorf("%s is %d lines (limit %d) — split it further", rel, n, limit)
		}
	}
}

// collectPartialPaths walks a subdirectory of CSSPartials collecting file paths.
func collectPartialPaths(dir string, out *[]string) {
	entries, err := CSSPartials.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := dir + "/" + e.Name()
		if e.IsDir() {
			collectPartialPaths(p, out)
		} else {
			*out = append(*out, p)
		}
	}
}

// compareAggregate returns a non-empty description of the first difference
// between the embedded aggregate and the freshly-concatenated partials, or ""
// if they match (modulo the generator's banner comment).
func compareAggregate(agg []byte, joined string) string {
	a := strings.TrimSpace(stripGeneratedBanner(string(agg)))
	j := strings.TrimSpace(joined)
	if a == j {
		return ""
	}
	as := strings.Split(a, "\n")
	js := strings.Split(j, "\n")
	n := len(as)
	if len(js) < n {
		n = len(js)
	}
	for i := 0; i < n; i++ {
		if as[i] != js[i] {
			return "first difference at line " + strconv.Itoa(i+1) +
				"\n  aggregate: " + trimLen(as[i], 80) +
				"\n  partials:  " + trimLen(js[i], 80)
		}
	}
	return "line-count mismatch (aggregate " + strconv.Itoa(len(as)) +
		" vs partials " + strconv.Itoa(len(js)) + ")"
}

// stripGeneratedBanner removes a leading /* ... */ banner comment.
func stripGeneratedBanner(css string) string {
	for {
		if !strings.HasPrefix(css, "/*") {
			return css
		}
		e := strings.Index(css[2:], "*/")
		if e < 0 {
			return css
		}
		css = css[2+e+2:]
	}
}

// nonEmptyLines returns the non-empty, trimmed lines of s in order, deduped.
func nonEmptyLines(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line != "" && !seen[line] {
			seen[line] = true
			out = append(out, line)
		}
	}
	return out
}

func trimLen(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
