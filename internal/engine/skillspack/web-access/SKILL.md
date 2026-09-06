---
name: web-access
description: "Fetch and read web content — pages, API/JSON data, downloads. Use whenever the task needs something from the web: looking up docs, calling an API, checking a URL, or reading an article — even if the user doesn't mention 'the web', 'curl', or a browser."
model-invocable: true
---

## Web Access

Two default fetch paths. Pick by content type, not by convenience — piping
JSON through lynx mangles it, and dumping raw HTML on a JS-heavy page yields
garbage.

### Web pages (human-readable HTML)

Pipe through lynx to read the rendered text:
```sh
curl --fail --max-time 30 "$URL" | lynx -dump -nolist -stdin
```

### API / JSON data

Fetch raw — never through lynx — and filter with jq only when you need to
filter or pretty-print:
```sh
curl --fail --max-time 30 "$URL" | jq .
```

## Gotchas

- **JS-rendered page**: lynx shows an empty or skeleton page. Try the
  underlying JSON/XHR endpoint (check for an API path or `?format=json`);
  fall back to asking the user or a headless renderer if available.
- **HTML arrives when JSON was expected**: the URL is probably a human page
  (login wall, redirect) — switch paths, don't force jq.
- **Non-2xx status**: `--fail` makes curl exit non-zero, so chain with `&&`
  and a 404 never feeds an error page into lynx/jq. To see the error,
  re-fetch *without* `--fail` — many APIs return the reason as JSON.
- **Auth**: pass headers explicitly, `curl -H "Authorization: Bearer $TOKEN"`
  — never paste secrets into URLs.
- **File download**: `curl --fail -L -o "$TMPDIR/name" "$URL"` — no lynx,
  no jq. Verify with `file` before use.
- **Paginated API**: loop with a page/offset variable, accumulate with
  `jq -s`, stop on an empty result set.
- **Slow endpoint**: `--max-time 30` is the ceiling; raise it only for
  known-slow endpoints, never as a habit.
