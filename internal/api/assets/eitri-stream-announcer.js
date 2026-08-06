// eitri-stream-announcer — browser island module for screen-reader stream
// announcements (issue #1071). Streaming replies are announced through a
// dedicated visually-hidden live region rather than the visible streaming
// bubble (which is re-rendered every ~80ms flush and would make assistive
// tech re-read the whole reply at token cadence). The announcer only ever
// receives *new* text deltas, throttled to ANNOUNCE_INTERVAL_MS and flushed
// on run completion.
(function () {
  'use strict';

  var S = window.eitriStream;

  // Pure delta bookkeeping: returns the pending announcement text plus the
  // stream-buffer offset already handed to the announcer. O(delta) per call —
  // never copies the whole buffer, so streaming flush performance is
  // unaffected no matter how long the reply grows.
  function accumulateStreamAnnounce(streamBuf, lastAnnouncedLen, pending) {
    if (lastAnnouncedLen >= streamBuf.length) {
      return { pending: pending, lastAnnouncedLen: lastAnnouncedLen };
    }
    return {
      pending: pending + streamBuf.substring(lastAnnouncedLen),
      lastAnnouncedLen: streamBuf.length,
    };
  }
  S.accumulateStreamAnnounce = accumulateStreamAnnounce;

  function ensureStreamAnnouncer() {
    var el = document.getElementById('stream-announcer');
    if (!el) {
      el = document.createElement('div');
      el.id = 'stream-announcer';
      el.className = 'sr-only';
      el.setAttribute('role', 'status');
      el.setAttribute('aria-live', 'polite');
      document.body.appendChild(el);
    }
    return el;
  }
  S.ensureStreamAnnouncer = ensureStreamAnnouncer;

  function clearStreamAnnounceTimer(state) {
    if (!state || !state.announceTimer) return;
    clearTimeout(state.announceTimer);
    state.announceTimer = null;
  }
  S.clearStreamAnnounceTimer = clearStreamAnnounceTimer;

  function flushStreamAnnounce(state) {
    clearStreamAnnounceTimer(state);
    if (!state || !state.announcePending) return;
    // Replacing textContent announces only the new delta (role="status" is
    // atomic): the region never accumulates the whole reply in the DOM.
    ensureStreamAnnouncer().textContent = state.announcePending;
    state.announcePending = '';
  }
  S.flushStreamAnnounce = flushStreamAnnounce;

  function queueStreamAnnounce(state) {
    if (!state) return;
    var acc = accumulateStreamAnnounce(state.streamBuf, state.lastAnnouncedLen, state.announcePending);
    state.announcePending = acc.pending;
    state.lastAnnouncedLen = acc.lastAnnouncedLen;
    if (!state.announcePending) return;
    if (!state.announceTimer) {
      state.announceTimer = window.setTimeout(function () {
        state.announceTimer = null;
        flushStreamAnnounce(state);
      }, S.ANNOUNCE_INTERVAL_MS);
    }
  }
  S.queueStreamAnnounce = queueStreamAnnounce;
})();
