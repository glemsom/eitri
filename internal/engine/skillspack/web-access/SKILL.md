---
name: web-access
description: "Fetch a URL: HTML as text via lynx, JSON via jq, a download via curl — and how to get past a blocked, empty or JS-only page."
model-invocable: true
---

## Web Access

Pick the fetch path by content type: JSON through lynx is mangled, and raw HTML read as prose is noise.

### Web pages (human-readable HTML)

```sh
curl -sS --fail -L --max-time 30 "$URL" | lynx -dump -nolist -stdin
```

### API / JSON data

Fetch raw, and filter with jq only for a field or a pretty print:

```sh
curl -sS --fail -L --max-time 30 "$URL" | jq .
```

### Downloads

```sh
curl -sS --fail -L --max-time 30 -o "$TMPDIR/name" "$URL" && file "$TMPDIR/name"
```

## Gotchas

- **`-sS`, never bare `-s`**: `-s` silences the progress meter and the error that `--fail` relies on; `-S` keeps the message.
- **A pipe hides curl's status.** `curl -f … | jq` exits 0 on a 404, because a pipeline reports its last command. When the fetch's outcome decides the result, lead with `set -o pipefail`.
- **Reading the failure**: `--fail` suppresses the body, so a 4xx looks empty. Many APIs return the reason as JSON — re-fetch without `--fail` to read it.
- **HTML where JSON was expected**: a login wall or redirect put you on a human page. Switch paths instead of forcing jq.
- **Empty, skeleton, or blocked**: lynx cannot run JavaScript, and some sites turn curl away with 403, 429, or a challenge page. Either way the HTML you got is not the content: retry with a browser `-A`, look for the data endpoint (`?format=json`, a `.json`/`.rss` variant, a path in the page source), then try a text proxy.
- **Paginated API**: loop `per_page=100&page=N`, stop when a page returns `[]`, count with `jq -s length`; or read the `Link: rel="next"`/`rel="last"` header for the page total.
