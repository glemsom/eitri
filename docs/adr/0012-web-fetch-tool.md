# 0012 — web_fetch tool

- **Status:** Accepted
- **Date:** 2025-07-17

## Context

Eitri's agent has no ability to read web content. When asked to look up API docs, read a GitHub page, or fetch any URL, it must guess from training data or ask the user to paste content. This limits the agent to workspace-local knowledge.

Adding a `web_fetch` tool gives the agent a way to read any public URL. A companion `web_search` tool (discover URLs automatically) is deferred.

## Decision

Add a built-in `web_fetch` tool in `internal/tool/web_fetch.go`:

1. **Single URL fetch** — accepts one `url` param. No batching.
2. **Semantic HTML extraction** — uses `goquery` to strip chrome (nav, footer, aside, script, style), extract title, and convert body to Markdown preserving headings, code blocks, links, and lists.
3. **32 KiB content cap** — prevents context-window blowup. Truncation marker appended when hit.
4. **15s default timeout** — configurable per-call via `timeout` param. Short enough to not stall agent turn.
5. **Plain HTTP** — no JS rendering. SPAs return minimal content. Acceptable trade-off for the initial scope.
6. **Proxy support** — `httpproxy.FromEnvironment().ProxyFunc()` reads `HTTP_PROXY`/`HTTPS_PROXY` env vars fresh on each request (unlike `http.ProxyFromEnvironment` which caches at process startup).
7. **No auth** — public URLs only. Cookie/header injection deferred.
8. **No search** — user provides the URL. `web_search` deferred to a separate decision.

## Consequences

- Positive: agent can read docs, GitHub pages, blogs, articles — any URL the user provides.
- Positive: tool follows established patterns in `internal/tool/` — no new infrastructure needed.
- Positive: `goquery` wraps `golang.org/x/net/html` which is already a transitive dep.
- Positive: fresh-read proxy config picks up runtime environment changes (e.g., proxy started after the process). `httpproxy.FromEnvironment()` is called on every HTTP request rather than once at startup.
- Negative: JS-heavy pages (SPA docs, React sites) return mostly empty content. Mitigation: documented limitation; can add chromedp fallback later.
- Negative: no search means the agent cannot discover URLs independently. Mitigation: deferred, not foreclosed.
- Negative: no caching means repeated fetches of the same URL do fresh HTTP requests each time. Mitigation: caching is premature optimization for now.
