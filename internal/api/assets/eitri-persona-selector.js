// eitri-persona-selector — Browser island for the persona selector dropdown.

(function () {
  'use strict';

  function init() {
    document.querySelectorAll('#persona-selector').forEach(function (el) {
      if (el.dataset.psInitialized) return;
      el.dataset.psInitialized = 'true';

      // TODO: Add event listeners for the custom dropdown
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  document.addEventListener('htmx:afterSwap', function () {
    init();
  });
})();
