// Eitri — Service Worker
// PWA installable standalone app shell

// The cache version embeds the asset cache-bust version (substituted at serve
// time by the server). Static asset URLs are content-addressed (?v=...), so a
// release both updates this script and invalidates the precache. (issue #969)
const CACHE = "eitri-__EITRI_VERSION__";

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(CACHE).then((cache) => {
      // Only core shell assets are precached. The heavy rendering libraries
      // (mermaid, KaTeX, Prism) are loaded on demand by eitri-lazy-load.js and
      // are cached by the cache-first handler below the first time they are
      // actually needed. (issue #968)
      return cache.addAll([
        "/",
        "/static/eitri.css?v=__EITRI_VERSION__",
        "/static/htmx.min.js?v=__EITRI_VERSION__",
        "/static/eitri-stream.js?v=__EITRI_VERSION__",
        "/static/eitri-composer.js?v=__EITRI_VERSION__",
        "/static/eitri-renderers.js?v=__EITRI_VERSION__",
        "/static/eitri-mermaid.js?v=__EITRI_VERSION__",
        "/static/eitri-lazy-load.js?v=__EITRI_VERSION__",
        "/static/eitri-persona-selector.js?v=__EITRI_VERSION__",
        "/static/eitri-session-rename.js?v=__EITRI_VERSION__",
        "/static/eitri-settings.js?v=__EITRI_VERSION__",
        "/static/eitri-context.js?v=__EITRI_VERSION__",
        "/static/eitri-resize.js?v=__EITRI_VERSION__",
        "/static/eitri-events.js?v=__EITRI_VERSION__",
        "/static/face.webp?v=__EITRI_VERSION__",
        "/static/favicon-32.png?v=__EITRI_VERSION__",
        "/static/favicon-16.png?v=__EITRI_VERSION__",
        "/static/pwa-icon-192.png?v=__EITRI_VERSION__",
        "/static/pwa-icon-512.png?v=__EITRI_VERSION__",
        "/manifest.json",
      ]);
    })
  );
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) => {
      return Promise.all(
        keys
          .filter((key) => key !== CACHE)
          .map((key) => caches.delete(key))
      );
    })
  );
  self.clients.claim();
});

self.addEventListener("fetch", (event) => {
  const url = new URL(event.request.url);

  // Network-only for API calls and SSE stream
  if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/stream")) {
    event.respondWith(fetch(event.request));
    return;
  }

  // Cache-first for static assets
  if (url.pathname.startsWith("/static/")) {
    event.respondWith(
      caches.open(CACHE).then((cache) => {
        return cache.match(event.request).then((cached) => {
          return cached || fetch(event.request).then((response) => {
            cache.put(event.request, response.clone());
            return response;
          });
        });
      })
    );
    return;
  }

  // Network-first for Google Fonts
  if (url.hostname === "fonts.googleapis.com" || url.hostname === "fonts.gstatic.com") {
    event.respondWith(
      fetch(event.request).then((response) => {
        const clone = response.clone();
        caches.open(CACHE).then((cache) => cache.put(event.request, clone));
        return response;
      }).catch(() => caches.match(event.request))
    );
    return;
  }

  // Navigation requests: try network, fall back to cached shell
  if (event.request.mode === "navigate") {
    event.respondWith(
      fetch(event.request).catch(() => caches.match("/"))
    );
    return;
  }

  // Default: network-only
  event.respondWith(fetch(event.request));
});
