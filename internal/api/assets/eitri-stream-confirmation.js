// eitri-stream-confirmation — browser island module for the blocked-read-path
// confirmation modal (issue #314) and its focus management / undo-toast flow
// (issue #1067). Shows a safety-first Allow/Deny dialog for paths requiring
// confirmation, restores focus on close, and swaps to a 5s-countdown undo toast
// on Deny. Fully idempotent so a late stream teardown can close an open modal.
(function () {
  'use strict';

  var S = window.eitriStream;

  function showConfirmationModal(sessionId, path, message) {
    closeConfirmationModal();

    // Remember which element had focus before the modal opened so it can be
    // restored when the modal closes (issue #1067).
    if (document.activeElement && document.activeElement !== document.body) {
      S.lastFocusedElement = document.activeElement;
    }

    S.activeConfirmation = { sessionId: sessionId, path: path, message: message };

    var overlay = document.createElement('div');
    overlay.id = 'confirmation-overlay';
    overlay.className = 'confirmation-overlay';

    overlay.setAttribute('aria-live', 'polite');

    overlay.innerHTML =
      '<div class="confirmation-modal" role="dialog" aria-modal="true" aria-labelledby="confirmation-title">' +
      '<h3 id="confirmation-title">&#9888; Path requires confirmation</h3>' +
      '<div class="confirmation-path">' + S.escapeHtml(path) + '</div>' +
      '<div class="confirmation-message">' + S.escapeHtml(message) + '</div>' +
      '<div class="confirmation-actions">' +
      '<button id="confirm-deny" class="confirm-deny" type="button">Deny</button>' +
      '<button id="confirm-allow" class="confirm-allow" type="button">Allow</button>' +
      '</div>' +
      '</div>';

    // Prevent clicks on overlay from closing (must choose Allow or Deny)
    overlay.addEventListener('click', function (e) {
      if (e.target === overlay) return;
    });

    document.body.appendChild(overlay);

    document.getElementById('confirm-allow').addEventListener('click', function () {
      resolveConfirmation(true);
    });

    document.getElementById('confirm-deny').addEventListener('click', function () {
      resolveConfirmation(false);
    });

    // Autofocus Deny button (safety-first default)
    document.getElementById('confirm-deny').focus();

    // Keyboard: focus trap, Enter on focused button, Escape denies
    overlay.addEventListener('keydown', confirmationKeyHandler);
  }
  S.showConfirmationModal = showConfirmationModal;

  function closeConfirmationModal() {
    var overlay = document.getElementById('confirmation-overlay');
    if (overlay) {
      overlay.removeEventListener('keydown', confirmationKeyHandler);
      overlay.remove();
    }
    S.activeConfirmation = null;

    // Restore focus to the element that opened the modal so keyboard users
    // always know where focus is after the flow (issue #1067).
    if (S.lastFocusedElement && S.lastFocusedElement.isConnected) {
      S.lastFocusedElement.focus();
    }
    S.lastFocusedElement = null;
  }
  S.closeConfirmationModal = closeConfirmationModal;

  function confirmationKeyHandler(e) {
    // Once the modal content is swapped for the undo toast, keyboard handling
    // is delegated to the toast's single Undo button (native Tab/Enter). In
    // particular Escape must not re-run deny, which would respawn the 5s
    // auto-close timer on top of the pending one (issue #1067).
    if (document.querySelector('.undo-toast')) return;

    var allowBtn = document.getElementById('confirm-allow');
    var denyBtn = document.getElementById('confirm-deny');

    if (e.key === 'Escape') {
      e.preventDefault();
      resolveConfirmation(false);
      return;
    }

    if (e.key === 'Tab') {
      if (!allowBtn || !denyBtn) return;
      var focusable = [denyBtn, allowBtn];
      var currentIndex = focusable.indexOf(document.activeElement);
      if (currentIndex === -1) return;

      e.preventDefault();
      if (e.shiftKey) {
        // Shift+Tab: reverse wrap
        var prevIndex = (currentIndex - 1 + focusable.length) % focusable.length;
        focusable[prevIndex].focus();
      } else {
        // Tab: forward wrap
        var nextIndex = (currentIndex + 1) % focusable.length;
        focusable[nextIndex].focus();
      }
      return;
    }

    if (e.key === 'Enter') {
      if (allowBtn && document.activeElement === allowBtn) {
        resolveConfirmation(true);
      }
      if (denyBtn && document.activeElement === denyBtn) {
        resolveConfirmation(false);
      }
    }
  }
  S.confirmationKeyHandler = confirmationKeyHandler;

  function resolveConfirmation(approved) {
    if (!S.activeConfirmation) return;

    var allowBtn = document.getElementById('confirm-allow');
    var denyBtn = document.getElementById('confirm-deny');
    if (allowBtn) allowBtn.disabled = true;
    if (denyBtn) denyBtn.disabled = true;

    var sessionId = S.activeConfirmation.sessionId;
    var path = S.activeConfirmation.path;

    if (approved) {
      // Allow: POST approved=true, close modal
      fetch('/api/sessions/' + encodeURIComponent(sessionId) + '/confirm', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: path, approved: true }),
      })
      .then(function (resp) {
        if (!resp.ok) {
          console.warn('Confirmation POST failed:', resp.status, resp.statusText);
        }
        closeConfirmationModal();
      })
      .catch(function (err) {
        console.warn('Confirmation POST error:', err);
        closeConfirmationModal();
      });
    } else {
      // Deny: POST approved=false, show undo toast with 5s countdown
      fetch('/api/sessions/' + encodeURIComponent(sessionId) + '/confirm', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: path, approved: false }),
      })
      .then(function (resp) {
        if (!resp.ok) {
          console.warn('Confirmation POST failed:', resp.status, resp.statusText);
        }
        showUndoToast(sessionId, path);
      })
      .catch(function (err) {
        console.warn('Confirmation POST error:', err);
        showUndoToast(sessionId, path);
      });
    }
  }
  S.resolveConfirmation = resolveConfirmation;

  function showUndoToast(sessionId, path) {
    var modal = document.querySelector('.confirmation-modal');
    if (!modal) return;

    // Replace modal content with undo toast
    modal.innerHTML =
      '<div class="undo-toast">' +
      '<div class="undo-toast-text">Access denied</div>' +
      '<div class="undo-toast-bar"></div>' +
      '<button class="undo-toast-btn" type="button">Undo</button>' +
      '</div>';

    var undoBtn = modal.querySelector('.undo-toast-btn');
    var undoTimeout = setTimeout(function () {
      closeConfirmationModal();
    }, 5000);

    if (undoBtn) {
      // Move focus to the Undo button so keyboard users know where they are
      // before the toast auto-closes (issue #1067).
      undoBtn.focus();

      undoBtn.addEventListener('click', function () {
        clearTimeout(undoTimeout);
        fetch('/api/sessions/' + encodeURIComponent(sessionId) + '/confirm', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path: path, approved: true }),
        })
        .then(function (resp) {
          if (!resp.ok) {
            console.warn('Undo POST failed:', resp.status, resp.statusText);
          }
          closeConfirmationModal();
        })
        .catch(function (err) {
          console.warn('Undo POST error:', err);
          closeConfirmationModal();
        });
      });
    }
  }
  S.showUndoToast = showUndoToast;
})();
