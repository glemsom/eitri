// eitri-stream — Browser island for managing SSE stream lifecycle.
// Handles EventSource connection, token display, tool cards, and render dispatch.

(function () {
  'use strict';

  // State per session
  const streams = new Map(); // sessionId -> { eventSource, state }

  const STATES = {
    IDLE: 'idle',
    CONNECTING: 'connecting',
    STREAMING: 'streaming',
    TOOL_RUNNING: 'tool-running',
    RENDERING: 'rendering',
    DONE: 'done',
    ERROR: 'error',
    RECONNECTING: 'reconnecting',
  };

  // Listen for HTMX trigger to connect stream
  document.addEventListener('eitri:connectRunStream', function (e) {
    const sessionId = e.detail || e.target?.value;
    if (!sessionId) return;
    connectStream(sessionId);
  });

  // Also listen for htmx:afterOnLoad to detect HX-Trigger with connectRunStream
  document.addEventListener('htmx:afterOnLoad', function (evt) {
    const headers = evt.detail?.xhr?.getResponseHeader?.('HX-Trigger');
    if (!headers) return;
    try {
      const parsed = JSON.parse(headers);
      if (parsed['eitri:connectRunStream']) {
        const sessionId = parsed['eitri:connectRunStream'];
        setTimeout(() => connectStream(sessionId), 50);
      }
    } catch (e) {
      // Not JSON or not relevant
    }
  });

  // Clean up on page unload
  document.addEventListener('htmx:beforeSwap', function (evt) {
    // If swapping away stream content, disconnect
    if (evt.detail?.target?.id === 'streaming') {
      disconnectAll();
    }
  });

  function connectStream(sessionId) {
    // Disconnect existing stream for this session
    disconnectStream(sessionId);

    const state = { status: STATES.CONNECTING };
    streams.set(sessionId, { eventSource: null, state });

    updateStreamIndicator(sessionId, state.status);

    const url = `/api/sessions/${sessionId}/stream`;
    const es = new EventSource(url);

    es.onopen = function () {
      state.status = STATES.STREAMING;
      updateStreamIndicator(sessionId, state.status);
      showStreamingBubble(sessionId);
    };

    es.onmessage = function (event) {
      try {
        const data = JSON.parse(event.data);
        handleSSEPacket(sessionId, data, state);
      } catch (e) {
        console.warn('Failed to parse SSE data:', e);
      }
    };

    es.onerror = function () {
      if (state.status === STATES.DONE || state.status === STATES.ERROR) {
        es.close();
        return;
      }
      // Attempt reconnect (EventSource does this automatically)
      state.status = STATES.RECONNECTING;
      updateStreamIndicator(sessionId, state.status);
    };

    const entry = streams.get(sessionId);
    if (entry) entry.eventSource = es;
    else streams.set(sessionId, { eventSource: es, state });
  }

  function disconnectStream(sessionId) {
    const entry = streams.get(sessionId);
    if (entry) {
      if (entry.eventSource) {
        entry.eventSource.close();
      }
      streams.delete(sessionId);
    }
    hideStreamIndicator(sessionId);
  }

  function disconnectAll() {
    for (const [id] of streams) {
      disconnectStream(id);
    }
  }

  function handleSSEPacket(sessionId, packet, state) {
    switch (packet.type) {
      case 'connecting':
        state.status = STATES.CONNECTING;
        updateStreamIndicator(sessionId, state.status);
        break;

      case 'token':
        state.status = STATES.STREAMING;
        appendToken(sessionId, packet.content);
        break;

      case 'tool_call':
        state.status = STATES.TOOL_RUNNING;
        updateStreamIndicator(sessionId, state.status);
        renderToolCard(sessionId, 'tool_call', packet);
        break;

      case 'tool_result':
        state.status = STATES.STREAMING;
        renderToolCard(sessionId, 'tool_result', packet);
        break;

      case 'component':
        renderComponent(sessionId, packet);
        break;

      case 'done':
        state.status = STATES.DONE;
        updateStreamIndicator(sessionId, state.status);
        finalizeMessage(sessionId, packet.message_id);
        disconnectStream(sessionId);
        break;

      case 'error':
        state.status = STATES.ERROR;
        renderError(sessionId, packet.message);
        disconnectStream(sessionId);
        break;

      case 'closed':
        disconnectStream(sessionId);
        break;
    }
  }

  let streamBuf = '';
  let streamTimer = null;
  const FLUSH_INTERVAL = 80; // ms

  function appendToken(sessionId, content) {
    streamBuf += content;

    // Flush on newline or timeout
    if (content.includes('\n') || content.includes('\n\n')) {
      flushStreamBuffer(sessionId);
      return;
    }

    if (!streamTimer) {
      streamTimer = setTimeout(() => {
        flushStreamBuffer(sessionId);
      }, FLUSH_INTERVAL);
    }
  }

  function flushStreamBuffer(sessionId) {
    if (streamTimer) {
      clearTimeout(streamTimer);
      streamTimer = null;
    }
    if (!streamBuf) return;

    const text = streamBuf;
    streamBuf = '';

    const el = document.getElementById('streaming');
    if (!el) return;

    // Use text content (no HTML injection) - just append text progressively
    // The final markdown render will replace this completely
    const span = document.createElement('span');
    span.textContent = text;
    el.appendChild(span);
  }

  function showStreamingBubble(sessionId) {
    const messages = document.getElementById('messages');
    if (!messages) return;

    // Create streaming container if it doesn't exist
    let el = document.getElementById('streaming');
    if (!el) {
      el = document.createElement('div');
      el.id = 'streaming';
      el.className = 'message message-assistant streaming-message';
      el.innerHTML = '<div class="message-avatar">E</div><div class="message-content streaming-indicator"><span></span></div>';
      messages.appendChild(el);
    }
  }

  function updateStreamIndicator(sessionId, status) {
    const el = document.getElementById('stream-indicator');
    if (!el) return;
    el.className = 'stream-indicator ' + status;
    el.textContent = status.charAt(0).toUpperCase() + status.slice(1).replace('-', ' ');
  }

  function hideStreamIndicator(sessionId) {
    const el = document.getElementById('stream-indicator');
    if (el) el.style.display = 'none';
  }

  function renderToolCard(sessionId, type, packet) {
    const messages = document.getElementById('messages');
    if (!messages) return;

    const formData = new FormData();
    formData.append('type', type);
    formData.append('tool', packet.tool || packet.name || '');
    if (packet.args) formData.append('args', JSON.stringify(packet.args));
    if (packet.output) formData.append('output', String(packet.output));
    if (packet.Args) formData.append('args', JSON.stringify(packet.Args));

    // Use HTMX to do the render
    const targetId = 'tool-cards-' + sessionId;
    let target = document.getElementById(targetId);
    if (!target) {
      target = document.createElement('div');
      target.id = targetId;
      target.className = 'tool-cards-container';
      messages.appendChild(target);
    }

    htmx.ajax('POST', `/api/sessions/${sessionId}/render/tool-card`, {
      source: document.body,
      target: '#' + targetId,
      swap: 'beforeend',
      values: Object.fromEntries(formData),
    });
  }

  function renderComponent(sessionId, packet) {
    // Stub for generative UI components (full implementation in issue #8)
    console.log('Component:', packet.name, packet.data);
  }

  function finalizeMessage(sessionId, messageId) {
    // Send message_id to render endpoint for goldmark conversion
    const streamingEl = document.getElementById('streaming');
    if (streamingEl) {
      streamingEl.style.opacity = '0.5';
      streamingEl.classList.add('rendering');
    }

    htmx.ajax('POST', `/api/sessions/${sessionId}/render/markdown`, {
      source: document.body,
      target: '#streaming',
      swap: 'outerHTML',
      values: { message_id: messageId || '' },
    });
  }

  function renderError(sessionId, message) {
    htmx.ajax('POST', `/api/sessions/${sessionId}/render/error`, {
      source: document.body,
      target: '#error-toasts',
      swap: 'beforeend',
      values: { message: message || 'An error occurred' },
    });
  }

  // ————— Code block copy buttons —————

  function initCodeBlockCopyButtons() {
    document.querySelectorAll('pre > code').forEach(function (codeEl) {
      var pre = codeEl.parentElement;
      if (pre.querySelector('.copy-btn')) return; // already has button

      var btn = document.createElement('button');
      btn.className = 'copy-btn';
      btn.textContent = 'Copy';
      btn.setAttribute('aria-label', 'Copy code');

      btn.addEventListener('click', function () {
        var text = codeEl.textContent || '';
        navigator.clipboard.writeText(text).then(function () {
          btn.textContent = 'Copied!';
          setTimeout(function () { btn.textContent = 'Copy'; }, 2000);
        }).catch(function () {
          btn.textContent = 'Failed';
          setTimeout(function () { btn.textContent = 'Copy'; }, 2000);
        });
      });

      pre.style.position = 'relative';
      pre.appendChild(btn);
    });
  }

  // Run on load and after HTMX swaps
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initCodeBlockCopyButtons);
  } else {
    initCodeBlockCopyButtons();
  }
  document.addEventListener('htmx:afterSwap', initCodeBlockCopyButtons);
  document.addEventListener('htmx:afterSettle', initCodeBlockCopyButtons);

})();
