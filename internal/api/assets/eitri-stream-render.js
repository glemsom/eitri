// eitri-stream-render — browser island module for final render dispatch.
// Owns the server calls that turn the streaming bubble into final rendered
// output: the Markdown render on 'done' (finalizeMessage) and error toasts on
// failure (renderError). Both POST to /api/sessions/{id}/render via HTMX and
// swap the result into the messages area.
(function () {
  'use strict';

  var S = window.eitriStream;

  function finalizeMessage(sessionId, messageId, usage, onRendered) {
    const streamingEl = document.getElementById('streaming');
    if (streamingEl) {
      streamingEl.style.opacity = '0.6';
      streamingEl.classList.add('rendering');
    }

    let completed = false;
    function finish() {
      if (completed) return;
      completed = true;
      document.body.removeEventListener('htmx:afterSwap', afterSwap);
      S.appendTokenUsage(usage);
      if (typeof onRendered === 'function') onRendered();
      if (S.onMessageFinalizedScroll) S.onMessageFinalizedScroll();
    }

    function afterSwap(evt) {
      const target = evt.detail && evt.detail.target;
      if (target && target.id === 'streaming') {
        // Small delay so the transient RENDERING status is observable by
        // the test before transitioning to DONE.  Without this, an instant
        // server response (common on CI) can cause the entire
        // RENDERING→DONE transition to complete within a single event-loop
        // turn, making it impossible to test (or even see) the intermediate
        // state.
        window.setTimeout(finish, 100);
      }
    }

    document.body.addEventListener('htmx:afterSwap', afterSwap);

    htmx.ajax('POST', '/api/sessions/' + sessionId + '/render', {
      source: document.body,
      target: '#streaming',
      swap: 'outerHTML',
      contentType: 'application/json',
      values: {
        kind: 'markdown',
        message_id: messageId || '',
      },
    });

    window.setTimeout(finish, 500);
  }
  S.finalizeMessage = finalizeMessage;

  function renderError(sessionId, message) {
    htmx.ajax('POST', '/api/sessions/' + sessionId + '/render', {
      source: document.body,
      target: '#error-toasts',
      swap: 'beforeend',
      contentType: 'application/json',
      values: {
        kind: 'error',
        message: message || 'An error occurred',
      },
    });
  }
  S.renderError = renderError;
})();
