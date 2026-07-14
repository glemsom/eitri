// eitri-session-rename — Browser island for inline session rename.
// Double-click session title to enter edit mode, blur/Enter to save.

(function () {
  'use strict';

  function initRename() {
    document.querySelectorAll('#session-tabs .session-title').forEach(function (titleEl) {
      if (titleEl.dataset.renameInitialized) return;
      titleEl.dataset.renameInitialized = 'true';

      titleEl.addEventListener('dblclick', function (e) {
        e.preventDefault();
        e.stopPropagation();

        var currentText = titleEl.textContent.trim();
        var input = document.createElement('input');
        input.type = 'text';
        input.className = 'session-rename-input';
        input.value = currentText;
        input.setAttribute('aria-label', 'Rename session');

        var parent = titleEl.parentElement;
        titleEl.style.display = 'none';
        parent.insertBefore(input, titleEl.nextSibling);
        input.focus();
        input.select();

        function finishRename() {
          var newTitle = input.value.trim();
          input.remove();
          titleEl.style.display = '';
          if (newTitle && newTitle !== currentText) {
            titleEl.textContent = newTitle;
          }
        }

        input.addEventListener('blur', finishRename);
        input.addEventListener('keydown', function (ev) {
          if (ev.key === 'Enter') {
            ev.preventDefault();
            input.blur();
          } else if (ev.key === 'Escape') {
            ev.preventDefault();
            input.value = currentText;
            input.blur();
          }
        });
      });
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initRename);
  } else {
    initRename();
  }

  document.addEventListener('htmx:afterSwap', function () {
    initRename();
  });
})();
