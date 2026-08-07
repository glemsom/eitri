// eitri-stream-scroll — browser island module for the optimistic user bubble
// and message auto-scroll (issue #95). Owns the scroll-to-bottom floating
// button (issue #104), the optimistic bubble inserted on chat submit, and the
// auto-scroll scheduling triggered as tokens stream in. Registers the shared
// S.onTokenActivity hook so the token/display module can kick a scroller here.
(function () {
  'use strict';

  var S = window.eitriStream;

  // Is the user currently at (or within a small margin of) the bottom of the
  // message list? We only auto-scroll when they are, so streaming content never
  // yanks the viewport away from an earlier read. Being at the bottom is the
  // default state, so this keeps normal behaviour while protecting the case
  // where the user scrolled up to read earlier output.
  function isNearBottom() {
    var messages = document.getElementById('messages');
    if (!messages) return true;
    return messages.scrollHeight - messages.scrollTop - messages.clientHeight < 120;
  }
  S.isNearBottom = isNearBottom;

  function smoothScrollToBottom() {
    var messages = document.getElementById('messages');
    var lastChild = messages && messages.lastElementChild;
    if (!lastChild) return;
    lastChild.scrollIntoView({ behavior: 'smooth', block: 'end' });
  }
  S.smoothScrollToBottom = smoothScrollToBottom;

  function autoScroll() {
    if (S.autoScrollPending) return;
    S.autoScrollPending = true;
    requestAnimationFrame(function () {
      S.autoScrollPending = false;
      var messages = document.getElementById('messages');
      if (!messages) return;
      if (!isNearBottom()) return;
      var lastChild = messages.lastElementChild;
      if (!lastChild) return;
      // Don't fight the user: if they scrolled up to read, hold position.
      // Instant (not smooth) during active streaming: queuing dozens of smooth
      // scroll animations on a large history is main-thread churn and freezes
      // the page. The final settled message and the manual button stay smooth.
      lastChild.scrollIntoView({ behavior: 'auto', block: 'end' });
    });
  }
  S.autoScroll = autoScroll;

  function scrollToLatest() {
    smoothScrollToBottom();
  }
  S.scrollToLatest = scrollToLatest;

  // Tokens module calls this after it renders streaming output so the viewport
  // follows the reply (matches the pre-split wrapper that glued appendToken /
  // showStreamingBubble to autoScroll).
  S.onTokenActivity = autoScroll;

  function insertOptimisticBubble(text) {
    const messages = document.getElementById('messages');
    if (!messages || !text) return;
    if (messages.querySelector('[data-optimistic="true"]')) return;
    const bubble = document.createElement('div');
    bubble.className = 'message message-user';
    bubble.setAttribute('data-optimistic', 'true');
    // Escape HTML then convert newlines to <br> for display (matches server-side nl2br)
    var safe = S.escapeHtml(text).replace(/\r?\n/g, '<br>\n');
    bubble.innerHTML = '<div class="message-body"><div class="message-content">' + safe + '</div></div><div class="message-avatar">U</div>';
    messages.appendChild(bubble);
  }
  S.insertOptimisticBubble = insertOptimisticBubble;

  function removeOptimisticBubbles() {
    var bubbles = document.querySelectorAll('[data-optimistic="true"]');
    for (var i = 0; i < bubbles.length; i++) {
      bubbles[i].remove();
    }
  }
  S.removeOptimisticBubbles = removeOptimisticBubbles;

  // ---- Scroll-to-bottom floating button (issue #104) ----

  function initScrollToBottomButton() {
    var sentinel = document.getElementById('scroll-sentinel');
    var btn = document.getElementById('scroll-to-bottom-btn');
    if (!sentinel || !btn) return;

    // Use IntersectionObserver to detect if user is at bottom
    var observer = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          btn.classList.remove('visible');
        } else {
          btn.classList.add('visible');
        }
      });
    }, {
      root: document.getElementById('messages'),
      threshold: 0
    });

    observer.observe(sentinel);
    sentinel._scrollObserver = observer;

    btn.addEventListener('click', function () {
      scrollToLatest();
      btn.classList.remove('visible');
    });
  }
  S.initScrollToBottomButton = initScrollToBottomButton;

  function reinitScrollObserver() {
    var sentinel = document.getElementById('scroll-sentinel');
    if (!sentinel) return;

    // Disconnect old observer if any
    if (sentinel._scrollObserver) {
      sentinel._scrollObserver.disconnect();
    }

    initScrollToBottomButton();
  }
  S.reinitScrollObserver = reinitScrollObserver;

  // Insert optimistic user bubble when chat form is about to submit
  document.addEventListener('htmx:configRequest', function (evt) {
    if (!evt.detail || !evt.detail.path) return;
    if (!/\/api\/sessions\/[^/]+\/chat$/.test(evt.detail.path)) return;
    var values = evt.detail.parameters || {};
    var message = values.message || values['message'] || '';
    if (message) {
      insertOptimisticBubble(message);
    }
  });

  // After any HTMX swap, remove optimistic bubbles and auto-scroll
  document.addEventListener('htmx:afterSwap', function (evt) {
    var targetId = evt.detail && evt.detail.target && evt.detail.target.id;
    if (targetId === 'messages' || targetId === 'streaming') {
      S.relocatePending = true;
      removeOptimisticBubbles();
      autoScroll();
    }
  });

  // Delayed scroll to the latest message once a final render lands (matches the
  // pre-split finalizeMessage wrapper which scheduled scrollToLatest after the
  // final onRendered callback).
  S.onMessageFinalizedScroll = function () {
    setTimeout(scrollToLatest, 100);
  };
})();
