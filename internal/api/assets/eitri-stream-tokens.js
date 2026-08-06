// eitri-stream-tokens — browser island module for token display and render
// dispatch. Owns the streaming output bubble: incremental markdown flushing
// (issue #1076/raw-tail path), paragraph-commit vs live-tail rendering, the
// token-usage footer, thinking-panel live deltas, and the code-block button
// decoration. Auto-scroll is triggered through the shared runtime hook
// (S.onTokenActivity) that eitri-stream.js registers.
(function () {
  'use strict';

  var S = window.eitriStream;

  function clearStreamTimer(state) {
    if (!state || !state.streamTimer) return;
    clearTimeout(state.streamTimer);
    state.streamTimer = null;
  }
  S.clearStreamTimer = clearStreamTimer;

  function appendToken(state, content) {
    state.streamBuf += content;

    if (content.indexOf('\n') !== -1) {
      flushStreamBuffer(state);
      if (S.onTokenActivity) S.onTokenActivity();
      return;
    }

    if (!state.streamTimer) {
      state.streamTimer = window.setTimeout(function () {
        flushStreamBuffer(state);
      }, S.FLUSH_INTERVAL);
    }
    if (S.onTokenActivity) S.onTokenActivity();
  }
  S.appendToken = appendToken;

  function flushStreamBuffer(state) {
    clearStreamTimer(state);
    if (!state.streamBuf) return;
    S.queueStreamAnnounce(state);

    const text = state.streamBuf;
    const el = document.getElementById('streaming');
    if (!el) return;

    const contentEl = el.querySelector('.message-content') || el;

    // Render incrementally instead of re-rendering the ENTIRE accumulated text
    // on every flush (which is O(total) per flush → O(n²) over a long stream and
    // freezes the main thread). Markdown blocks are delimited by '\n\n', each
    // block is independent, so commit completed blocks to the DOM once and only
    // re-render the trailing *still-growing* block on each flush.
    const BP = '\n\n';
    const lastBoundary = text.lastIndexOf(BP);
    const base = lastBoundary >= 0 ? text.substring(0, lastBoundary + BP.length) : '';
    const tail = lastBoundary >= 0 ? text.substring(lastBoundary + BP.length) : text;

    if (base.length > state.renderedBase.length) {
      // Commit newly-completed block(s) into stable DOM nodes, once. A new
      // still-growing block follows, so drop any stale tail element first.
      const newRaw = base.substring(state.renderedBase.length);
      const holder = document.createElement('div');
      holder.innerHTML = S.lightweightMarkdown(newRaw);
      const staleTail = contentEl.querySelector('.stream-tail');
      if (staleTail) staleTail.remove();
      while (holder.firstChild) contentEl.appendChild(holder.firstChild);
      state.renderedBase = base;
    }

    if (!tail) return;

    let tailEl = contentEl.querySelector('.stream-tail');
    if (!tailEl) {
      tailEl = document.createElement('div');
      tailEl.className = 'stream-tail';
      contentEl.appendChild(tailEl);
    }

    if (tail.length <= S.STREAM_TAIL_RAW_LIMIT) {
      // Small live block: keep inline markdown formatting (current behaviour).
      tailEl.innerHTML = S.lightweightMarkdown(tail);
      tailEl.dataset.rawMode = '';
      tailEl.dataset.rawLen = '';
      return;
    }

    // Large single block (e.g. a big streamed code block): append-only so each
    // flush is O(delta) instead of O(total). The first flush past the limit
    // seeds the element with the (small, ≤limit) escaped text so far; every
    // later flush appends only the new substring as a text node. Exact markdown
    // is produced by the server render on 'done', so the final message is
    // correctly formatted here too.
    if (!tailEl.dataset.rawMode) {
      tailEl.dataset.rawMode = '1';
      tailEl.classList.add('stream-tail-raw');
      tailEl.innerHTML = S.escapeHtml(tail);
      tailEl.dataset.rawLen = String(tail.length);
      return;
    }
    const prev = parseInt(tailEl.dataset.rawLen || '0', 10) || 0;
    if (tail.length > prev) {
      tailEl.appendChild(document.createTextNode(tail.substring(prev)));
      tailEl.dataset.rawLen = String(tail.length);
    }
  }
  S.flushStreamBuffer = flushStreamBuffer;

  function showStreamingBubble() {
    const messages = document.getElementById('messages');
    if (!messages) return;

    var sentinel = document.getElementById('scroll-sentinel');

    // Relocate any elements an HTMX swap left past the scroll-sentinel back to
    // their correct position (user bubbles before streaming, assistant/tool
    // elements after). This walk is O(history), so it only runs when an actual
    // swap was observed (relocatePending armed), never on every streaming flush.
    if (S.relocatePending && sentinel && sentinel.parentNode === messages) {
      S.relocatePending = false;
      var after = sentinel.nextElementSibling;
      while (after) {
        var next = after.nextElementSibling;
        var streaming = document.getElementById('streaming');
        if (after.classList.contains('message-user') && streaming) {
          messages.insertBefore(after, streaming);
        } else {
          messages.insertBefore(after, sentinel);
        }
        after = next;
      }
    }

    let el = document.getElementById('streaming');
    if (!el) {
      el = document.createElement('div');
      el.id = 'streaming';
      if (sentinel && sentinel.parentNode === messages) {
        messages.insertBefore(el, sentinel);
      } else {
        messages.appendChild(el);
      }
    }

    if (!el.classList.contains('message-assistant')) {
      el.className = 'message message-assistant streaming-message';
      // Static assets are served immutable; the cache-bust version rendered on
      // <body data-asset-version> keeps runtime-built asset URLs from going
      // stale. (issue #969)
      var assetVersion = document.body && document.body.getAttribute('data-asset-version') || 'dev';
      el.innerHTML = '<span class="message-avatar-container"><img class="message-avatar" src="/static/face.webp?v=' + assetVersion + '" alt="Eitri" width="32" height="32"></span><div class="message-body"><div class="message-content"></div></div>';
    }
    if (S.onTokenActivity) S.onTokenActivity();
  }
  S.showStreamingBubble = showStreamingBubble;

  function clearThinkingPanel() {
    var el = document.querySelector('#thinking-panel .thinking-content');
    if (el) el.textContent = '';
  }
  S.clearThinkingPanel = clearThinkingPanel;

  // Live reasoning/thinking content. Appends incrementally as a text node
  // instead of rewriting the whole accumulated text (el.textContent += is
  // O(total) DOM serialise+replace per event and freezes the main thread on
  // long reasoning streams). Scroll is batched to a rAF — forcing
  // el.scrollTop = el.scrollHeight sync-layouts the whole growing sidebar
  // tree on every delta, also O(n²).
  //
  // The transcript is also BOUNDED: reasoning models (e.g. deepseek) can emit
  // hundreds of KB of reasoning as a single growing block. Even with per-
  // delta text-node appends, holding the entire transcript as one live text
  // node forces the browser to re-wrap/re-layout the whole thing on every
  // frame during streaming — O(n²) main-thread layout that freezes the page
  // (gear/nav unclickable, Chrome "kill page"). We keep only the trailing
  // `thinkingPanelMaxText` characters (dropping oldest leading text), which
  // is exactly what a live auto-scrolled transcript needs to show.
  function appendThinkingDelta(content) {
    var el = document.querySelector('#thinking-panel .thinking-content');
    if (!el) return;
    el.appendChild(document.createTextNode(content));

    // Trim the oldest reasoning once the accumulated transcript exceeds the
    // budget so each frame's re-layout stays cheap. Budget-bounded scans are
    // O(cap) per append, not O(total).
    if (el.textContent.length > S.thinkingPanelMaxText) {
      while (el.textContent.length > S.thinkingPanelMaxText && el.firstChild) {
        el.removeChild(el.firstChild);
      }
    }

    // Auto-scroll to bottom as content arrives, coalesced to one per frame.
    if (!S.thinkingScrollPending) {
      S.thinkingScrollPending = true;
      requestAnimationFrame(function () {
        S.thinkingScrollPending = false;
        if (el.isConnected) el.scrollTop = el.scrollHeight;
      });
    }
  }
  S.appendThinkingDelta = appendThinkingDelta;

  function appendTokenUsage(usage) {
    const messages = document.getElementById('messages');
    if (!messages) return;

    const existing = document.getElementById('token-usage');
    if (existing) existing.remove();

    const footer = document.createElement('div');
    footer.id = 'token-usage';
    footer.className = 'token-usage text-muted';

    if (usage && usage.total_tokens) {
      footer.textContent = 'Tokens: ' + usage.total_tokens + ' (prompt: ' + usage.prompt_tokens + ', completion: ' + usage.completion_tokens + ')';
    } else {
      let estimatedTotal = 1;
      if (messages) {
        estimatedTotal = Math.max(1, Math.floor((messages.textContent || '').length / 4));
      }
      footer.textContent = 'Tokens: ~' + estimatedTotal + ' (estimate)';
    }
    // Insert before scroll-sentinel so sentinel remains last child for IntersectionObserver
    var sentinel = document.getElementById('scroll-sentinel');
    if (sentinel && sentinel.parentNode === messages) {
      messages.insertBefore(footer, sentinel);
    } else {
      messages.appendChild(footer);
    }
  }
  S.appendTokenUsage = appendTokenUsage;

  function initCodeBlockButtons() {
    document.querySelectorAll('pre > code').forEach(function (codeEl) {
      const pre = codeEl.parentElement;
      if (pre.dataset.cbInitialized) return;
      pre.dataset.cbInitialized = 'true';
      pre.style.position = 'relative';

      const copyBtn = document.createElement('button');
      copyBtn.className = 'code-btn copy-btn';
      copyBtn.textContent = 'Copy';
      copyBtn.setAttribute('aria-label', 'Copy code');
      copyBtn.addEventListener('click', function () {
        const text = codeEl.textContent || '';
        navigator.clipboard.writeText(text).then(function () {
          copyBtn.textContent = 'Copied!';
          setTimeout(function () { copyBtn.textContent = 'Copy'; }, 2000);
        }).catch(function () {
          copyBtn.textContent = 'Failed';
          setTimeout(function () { copyBtn.textContent = 'Copy'; }, 2000);
        });
      });
      pre.appendChild(copyBtn);

      const wrapBtn = document.createElement('button');
      wrapBtn.className = 'code-btn wrap-btn';
      wrapBtn.textContent = 'Wrap';
      wrapBtn.setAttribute('aria-label', 'Toggle line wrap');
      wrapBtn.addEventListener('click', function () {
        const isWrapped = pre.classList.toggle('code-wrapped');
        wrapBtn.textContent = isWrapped ? 'No wrap' : 'Wrap';
      });
      pre.appendChild(wrapBtn);

      const lines = codeEl.textContent.split('\n').length;
      if (lines > 500) {
        pre.classList.add('code-collapsed');
        const showAllBtn = document.createElement('button');
        showAllBtn.className = 'code-btn show-all-btn';
        showAllBtn.textContent = 'Show all (' + lines + ' lines)';
        showAllBtn.setAttribute('aria-label', 'Show full content');
        showAllBtn.addEventListener('click', function () {
          pre.classList.remove('code-collapsed');
          showAllBtn.textContent = 'Collapse';
          showAllBtn.addEventListener('click', function () {
            pre.classList.add('code-collapsed');
            showAllBtn.textContent = 'Show all (' + lines + ' lines)';
          }, { once: true });
        }, { once: true });
        pre.appendChild(showAllBtn);
      }
    });
  }
  S.initCodeBlockButtons = initCodeBlockButtons;
})();
