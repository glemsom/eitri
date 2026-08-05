// Eitri — Service Worker
// PWA installable standalone app shell

// Cache version is bumped when the precache asset list changes so browsers
// with an old service worker drop their stale cache on activate.
const CACHE = "eitri-v2";

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(CACHE).then((cache) => {
      // Only core shell assets are precached. The heavy rendering libraries
      // (mermaid, KaTeX, Prism) are loaded on demand by eitri-lazy-load.js and
      // are cached by the cache-first handler below the first time they are
      // actually needed. (issue #968)
      return cache.addAll([
        "/",
        "/static/eitri.css",
        "/static/htmx.min.js",
        "/static/eitri-stream.js",
        "/static/eitri-composer.js",
        "/static/eitri-renderers.js",
        "/static/eitri-mermaid.js",
        "/static/eitri-lazy-load.js",
        "/static/eitri-persona-selector.js",
        "/static/eitri-session-rename.js",
        "/static/eitri-settings.js",
        "/static/eitri-context.js",
        "/static/eitri-resize.js",
        "/static/eitri-events.js",
        "/static/face.webp",
        "/static/favicon-32.png",
        "/static/favicon-16.png",
        "/static/pwa-icon-192.png",
        "/static/pwa-icon-512.png",
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
