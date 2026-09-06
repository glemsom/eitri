---
name: web-access
description: Fetch and read web content — rendered pages via curl+lynx, API/JSON data via raw curl+jq — including edge cases (JS-heavy pages, auth, downloads, pagination, failure handling).
model-invocable: true
---

## Web Access

Two fetch paths. Pick by content type, not by convenience — piping JSON
through lynx mangles it, and dumping raw HTML on a JS-heavy page yields
garbage.

### Web pages (human-readable HTML)

Pipe through lynx to read the rendered text, always with `-nolist` and
`-stdin`:
```sh
curl --fail --max-time 30 "$URL" | lynx -dump -nolist -stdin
```

### API / JSON data

Fetch raw — never through lynx — and filter with jq:
```sh
curl --fail --max-time 30 "$URL" | jq .
```
Pipe to jq only when you need to filter or pretty-print; plain `curl` output
is fine when the JSON is small or you post-process it yourself.

## Decision guide

| Situation | Approach |
|---|---|
| Article / docs / plain HTML | curl + lynx (above) |
| REST/JSON endpoint | curl + jq (above) |
| Page renders via JavaScript | lynx shows an empty or skeleton page. Try the underlying JSON/XHR endpoint (check for an API path, or `?format=json`); fall back to asking the user, or a headless renderer if available. |
| Endpoint needs auth | Pass headers explicitly: `curl -H "Authorization: Bearer $TOKEN" ...`. Never paste secrets into URLs. |
| File download | `curl --fail -L -o "$TMPDIR/name" "$URL"` — no lynx, no jq. Verify with `file` before use. |
| Paginated API | Loop with a page/offset variable, accumulate with `jq -s`, stop on an empty result set. |

## Failure handling

- `--fail` makes curl exit non-zero on HTTP errors; chain with `&&` so a
  404 never feeds an error page into lynx/jq.
- On non-2xx, re-fetch *without* `--fail` and inspect the body — many APIs
  return the reason as JSON.
- `--max-time 30` is the default ceiling; raise it only for known-slow
  endpoints, never as a habit.
- If a fetch returns HTML when JSON was expected, the URL is probably a
  human page (login wall, redirect) — switch paths, don't force jq.
