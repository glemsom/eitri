// eitri-lazy-load — On-demand loader for the heavy rendering libraries.
//
// Mermaid (2.7MB), KaTeX and Prism used to be loaded synchronously in the
// document head on every page load, so every page parsed ~4.7MB of JavaScript
// before first paint. This island fetches those libraries only when the page
// (or a fragment swapped in later via HTMX) actually contains content that
// needs them — a rendered diagram, an equation, or a code block.
//
// The library scripts themselves only *define* globals; the renderer islands
// (eitri-mermaid.js, eitri-renderers.js) do the actual rendering and already
// tolerate missing libraries. Once a library arrives we dispatch a custom
// event so those islands run exactly as they would have on a full page load.
//
// If a library fails to load (offline, blocked request, server hiccup) the
// rejection is caught here — never left as an unhandled promise rejection —
// logged once, and surfaced to the renderer islands via a *-load-failed event
// so content degrades to a visible fallback instead of silently losing the
// diagram, equation, or syntax highlighting (issue #1078).
(function () {
  'use strict';

  var loaded = {
    mermaid: false,
    katex: false,
    prism: false,
  };

  // A failed load is permanent for the page lifetime: re-scanning after HTMX
  // swaps must not re-attempt the fetch and spam the console on every swap.
  var failed = {
    mermaid: false,
    katex: false,
    prism: false,
  };

  function hasMermaidContent() {
    return !!document.querySelector('pre.mermaid');
  }

  function hasKatexContent() {
    return !!document.querySelector('.math-inline, .math-block');
  }

  function hasPrismContent() {
    return !!document.querySelector('pre code');
  }

  // Static assets are served with a long-lived immutable Cache-Control, so
  // every URL must carry the cache-bust version rendered by the page shell
  // (<body data-asset-version>) — otherwise a released asset change would never
  // be picked up. (issue #969)
  function assetUrl(path) {
    var v = document.body && document.body.getAttribute('data-asset-version');
    return path + '?v=' + (v || 'dev');
  }

  function loadCss(href) {
    var link = document.createElement('link');
    link.rel = 'stylesheet';
    link.href = href;
    document.head.appendChild(link);
  }

  function loadScript(src) {
    return new Promise(function (resolve, reject) {
      var s = document.createElement('script');
      s.src = src;
      s.async = true;
      s.onload = resolve;
      s.onerror = function () {
        reject(new Error('failed to load ' + src));
      };
      document.head.appendChild(s);
    });
  }

  // Shared failure path: logs the failure exactly once per library (subsequent
  // scans are no-ops) and tells the renderer islands to degrade their content
  // with a visible message/fallback. Because every load*() promise is caught
  // here, scan() can fire-and-forget without ever producing an unhandled
  // promise rejection in the console. (issue #1078)
  function handleLoadFailure(name, failEvent, err) {
    if (failed[name]) return;
    failed[name] = true;
    console.error('eitri-lazy-load: ' + name + ' failed to load (' + err.message + '); ' + name + ' content will not be rendered');
    document.dispatchEvent(new CustomEvent(failEvent));
  }

  function loadMermaid() {
    if (loaded.mermaid) return Promise.resolve();
    loaded.mermaid = true;
    return loadScript(assetUrl('/static/mermaid.min.js'))
      .then(function () {
        document.dispatchEvent(new CustomEvent('eitri:mermaid-loaded'));
      })
      .catch(function (err) {
        handleLoadFailure('mermaid', 'eitri:mermaid-load-failed', err);
      });
  }

  function loadKatex() {
    if (loaded.katex) return Promise.resolve();
    loaded.katex = true;
    loadCss(assetUrl('/static/katex.min.css'));
    return loadScript(assetUrl('/static/katex.min.js'))
      .then(function () {
        document.dispatchEvent(new CustomEvent('eitri:katex-loaded'));
      })
      .catch(function (err) {
        handleLoadFailure('katex', 'eitri:katex-load-failed', err);
      });
  }

  function loadPrism() {
    if (loaded.prism) return Promise.resolve();
    loaded.prism = true;
    loadCss(assetUrl('/static/prism.min.css'));
    // Prism's core must be present before the Go grammar component registers
    // itself, so the two are loaded strictly in sequence.
    return loadScript(assetUrl('/static/prism-core.min.js'))
      .then(function () { return loadScript(assetUrl('/static/prism-go.min.js')); })
      .then(function () {
        document.dispatchEvent(new CustomEvent('eitri:prism-loaded'));
      })
      .catch(function (err) {
        handleLoadFailure('prism', 'eitri:prism-load-failed', err);
      });
  }

  function scan() {
    if (hasMermaidContent()) {
      loadMermaid();
    }
    if (hasKatexContent()) {
      loadKatex();
    }
    if (hasPrismContent()) {
      loadPrism();
    }
  }

  // Runs after the document is parsed (defer) and again after every HTMX swap,
  // so content rendered server-side or swapped in later triggers loading.
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', scan);
  } else {
    scan();
  }
  document.addEventListener('htmx:afterSwap', scan);
  document.addEventListener('htmx:afterSettle', scan);
})();
