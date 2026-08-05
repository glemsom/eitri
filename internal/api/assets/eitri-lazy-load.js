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
(function () {
  'use strict';

  var loaded = {
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
        console.error('eitri-lazy-load: failed to load ' + src);
        reject(new Error('failed to load ' + src));
      };
      document.head.appendChild(s);
    });
  }

  function loadMermaid() {
    if (loaded.mermaid) return Promise.resolve();
    loaded.mermaid = true;
    return loadScript('/static/mermaid.min.js').then(function () {
      document.dispatchEvent(new CustomEvent('eitri:mermaid-loaded'));
    });
  }

  function loadKatex() {
    if (loaded.katex) return Promise.resolve();
    loaded.katex = true;
    loadCss('/static/katex.min.css');
    return loadScript('/static/katex.min.js').then(function () {
      document.dispatchEvent(new CustomEvent('eitri:katex-loaded'));
    });
  }

  function loadPrism() {
    if (loaded.prism) return Promise.resolve();
    loaded.prism = true;
    loadCss('/static/prism.min.css');
    // Prism's core must be present before the Go grammar component registers
    // itself, so the two are loaded strictly in sequence.
    return loadScript('/static/prism-core.min.js')
      .then(function () { return loadScript('/static/prism-go.min.js'); })
      .then(function () {
        document.dispatchEvent(new CustomEvent('eitri:prism-loaded'));
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
