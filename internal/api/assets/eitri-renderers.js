// eitri-renderers — Code block, Prism, and KaTeX hooks.
// Runs on load and after HTMX swaps. Tolerates missing optional libraries.
(function () {
  function bindCodeBlocks() {
    document.querySelectorAll('pre[data-code-enhanced]').forEach(function (pre) {
      if (pre.dataset.eitriBound === 'true') return;
      pre.dataset.eitriBound = 'true';

      var codeEl = pre.querySelector('code');
      if (!codeEl) return;
      pre.style.position = 'relative';

      var copyBtn = pre.querySelector('.copy-btn');
      if (copyBtn) {
        copyBtn.addEventListener('click', function () {
          var text = codeEl.textContent || '';
          navigator.clipboard.writeText(text).then(function () {
            var original = copyBtn.textContent;
            copyBtn.textContent = 'Copied!';
            setTimeout(function () { copyBtn.textContent = original || 'Copy'; }, 2000);
          }).catch(function () {
            copyBtn.textContent = 'Failed';
            setTimeout(function () { copyBtn.textContent = 'Copy'; }, 2000);
          });
        });
      }

      var wrapBtn = pre.querySelector('.wrap-btn');
      if (wrapBtn) {
        wrapBtn.addEventListener('click', function () {
          var wrapped = pre.classList.toggle('code-wrapped');
          wrapBtn.textContent = wrapped ? 'No wrap' : 'Wrap';
        });
      }

      var showAllBtn = pre.querySelector('.show-all-btn');
      if (showAllBtn) {
        showAllBtn.addEventListener('click', function () {
          var collapsed = pre.classList.toggle('code-collapsed');
          var lines = pre.getAttribute('data-line-count') || '';
          showAllBtn.textContent = collapsed ? ('Show all (' + lines + ' lines)') : 'Collapse';
        });
      }
    });
  }

  function initPrism() {
    if (typeof Prism === 'undefined' || typeof Prism.highlightElement !== 'function') return;
    document.querySelectorAll('pre code').forEach(function (codeEl) {
      if (codeEl.closest('pre.mermaid')) return;
      if (codeEl.dataset.prismProcessed === 'true') return;
      Prism.highlightElement(codeEl);
      codeEl.dataset.prismProcessed = 'true';
    });
  }

  function renderKatexElement(el, displayMode) {
    if (typeof katex === 'undefined' || typeof katex.render !== 'function') return;
    if (el.dataset.katexProcessed === 'true') return;
    var latex = el.getAttribute('data-latex') || '';
    if (!latex) return;
    try {
      katex.render(latex, el, {
        throwOnError: false,
        displayMode: displayMode,
      });
      el.dataset.katexProcessed = 'true';
    } catch (err) {
      console.warn('KaTeX render failed:', err);
    }
  }

  function initKatex() {
    document.querySelectorAll('.math-inline').forEach(function (el) {
      renderKatexElement(el, false);
    });
    document.querySelectorAll('.math-block').forEach(function (el) {
      renderKatexElement(el, true);
    });
  }

  function initAll() {
    bindCodeBlocks();
    initPrism();
    initKatex();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initAll);
  } else {
    initAll();
  }
  document.addEventListener('htmx:afterSwap', initAll);
  document.addEventListener('htmx:afterSettle', initAll);
})();
