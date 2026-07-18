// eitri-stream — Browser island for managing SSE stream lifecycle.
// Handles EventSource connection, token display, tool cards, and render dispatch.

(function () {
  'use strict';

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

  const FLUSH_INTERVAL = 80;
  const NO_DEAD_AIR_MS = 650;

  function extractSessionId(detail, target) {
    if (typeof detail === 'string') return detail;
    if (detail && typeof detail.value === 'string') return detail.value;
    if (detail && typeof detail.sessionId === 'string') return detail.sessionId;
    if (target && typeof target.value === 'string') return target.value;
    return '';
  }

  var toolCardTimers = {}; // toolCallKey -> interval ID
  var toolCardElapsed = {}; // toolCardKey -> {startMs, finalMs}
  var toolEntryCounter = 0; // monotonic counter for unique tool keys

  function clearToolActivity() {
    var list = document.querySelector('#tool-activity .tool-activity-list');
    if (list) list.innerHTML = '';
  }

  function createStreamState() {
    return {
      status: STATES.IDLE,
      firstEventSeen: false,
      awaitingResume: false,
      streamBuf: '',
      streamTimer: null,
      deadAirTimer: null,
    };
  }

  function resetActivityTracking() {
    toolCardTimers = {};
    toolCardElapsed = {};
  }

  function clearThinkingPanel() {
    var el = document.querySelector('#thinking-panel .thinking-content');
    if (el) el.textContent = '';
  }

  // Format for tool card live timer (issue #134)
  // Sub-second: '0.3s', under 1m: '1.2s', under 1h: '45s', over 1h: '2m 13s'
  function formatTimer(ms) {
    if (ms < 1000) return (ms / 1000).toFixed(1) + 's';
    if (ms < 60000) return (ms / 1000).toFixed(1) + 's';
    return Math.floor(ms / 60000) + 'm ' + Math.floor((ms % 60000) / 1000) + 's';
  }

  function statusLabel(status) {
    switch (status) {
      case STATES.IDLE:
        return 'Idle';
      case STATES.CONNECTING:
        return 'Connecting';
      case STATES.STREAMING:
        return 'Streaming';
      case STATES.TOOL_RUNNING:
        return 'Tool running';
      case STATES.RENDERING:
        return 'Rendering';
      case STATES.DONE:
        return 'Done';
      case STATES.ERROR:
        return 'Error';
      case STATES.RECONNECTING:
        return 'Reconnecting';
      default:
        return 'Idle';
    }
  }

  function defaultStatusDetail(status, state) {
    switch (status) {
      case STATES.IDLE:
        return 'Ready for next run.';
      case STATES.CONNECTING:
        if (state && !state.firstEventSeen) {
          return 'Connecting to run stream.';
        }
        return 'Waiting for stream to resume.';
      case STATES.STREAMING:
        return 'Receiving assistant response.';
      case STATES.TOOL_RUNNING:
        return 'Tool activity in progress.';
      case STATES.RENDERING:
        return 'Rendering final assistant message.';
      case STATES.DONE:
        return 'Run complete.';
      case STATES.ERROR:
        return 'Run failed.';
      case STATES.RECONNECTING:
        return 'Connection dropped. Waiting to resume stream.';
      default:
        return '';
    }
  }

  function updateRunStatus(status, detail, state) {
    const indicator = document.getElementById('stream-indicator');
    if (!indicator) return;

    indicator.className = 'stream-indicator ' + status;
    indicator.textContent = statusLabel(status);
  }

  function ensureChatChrome() {
    const indicator = document.getElementById('stream-indicator');
    if (!indicator) return;
    if (!indicator.textContent || !indicator.textContent.trim()) {
      updateRunStatus(STATES.IDLE, defaultStatusDetail(STATES.IDLE), null);
    }
  }

  function clearDeadAirTimer(state) {
    if (!state || !state.deadAirTimer) return;
    clearTimeout(state.deadAirTimer);
    state.deadAirTimer = null;
  }

  function armDeadAirTimer(state) {
    clearDeadAirTimer(state);
    state.deadAirTimer = window.setTimeout(function () {
      if (!state.firstEventSeen && state.status === STATES.CONNECTING) {
        updateRunStatus(STATES.CONNECTING, 'Working — waiting for first response or tool activity.', state);
      }
    }, NO_DEAD_AIR_MS);
  }

  function clearStreamTimer(state) {
    if (!state || !state.streamTimer) return;
    clearTimeout(state.streamTimer);
    state.streamTimer = null;
  }

  document.addEventListener('eitri:connectRunStream', function (e) {
    const sessionId = extractSessionId(e.detail, e.target);
    if (!sessionId) return;
    // Clear any persisted context data for this session when a new run starts
    try {
      sessionStorage.removeItem('eitri-context-' + sessionId);
    } catch (e) {
      // ignore
    }
    connectStream(sessionId);
  });

  function reenableComposer() {
    const input = document.getElementById('chat-input');
    const sendBtn = document.getElementById('send-btn');
    const stopBtn = document.getElementById('stop-btn');
    if (input) {
      input.disabled = false;
      input.focus();
    }
    if (sendBtn) {
      sendBtn.disabled = false;
      sendBtn.classList.remove('send-hidden');
    }
    if (stopBtn) {
      stopBtn.classList.add('stop-hidden');
    }
  }

  document.addEventListener('htmx:beforeSwap', function (evt) {
    const targetId = evt.detail && evt.detail.target && evt.detail.target.id;
    if (targetId === 'app' || targetId === 'chat-view' || targetId === 'streaming') {
      disconnectAll();
    }
  });

  function connectStream(sessionId) {
    disconnectStream(sessionId);
    stopAllToolCardTimers();
    resetActivityTracking();
    clearThinkingPanel();
    clearToolActivity();

    const state = createStreamState();
    state.status = STATES.CONNECTING;
    streams.set(sessionId, { eventSource: null, state });
    updateRunStatus(STATES.CONNECTING, defaultStatusDetail(STATES.CONNECTING, state), state);
    armDeadAirTimer(state);

    const es = new EventSource('/api/sessions/' + sessionId + '/stream');

    es.onopen = function () {
      if (state.awaitingResume) {
        updateRunStatus(STATES.RECONNECTING, 'Reconnected. Waiting for stream to resume.', state);
        return;
      }
      updateRunStatus(STATES.CONNECTING, defaultStatusDetail(STATES.CONNECTING, state), state);
    };

    es.onmessage = function (event) {
      try {
        const data = JSON.parse(event.data);
        handleSSEPacket(sessionId, data, state);
      } catch (err) {
        console.warn('Failed to parse SSE data:', err);
      }
    };

    es.onerror = function () {
      if (state.status === STATES.DONE || state.status === STATES.ERROR) {
        es.close();
        return;
      }
      clearDeadAirTimer(state);
      state.awaitingResume = state.firstEventSeen;
      state.status = STATES.RECONNECTING;
      updateRunStatus(STATES.RECONNECTING, defaultStatusDetail(STATES.RECONNECTING, state), state);
    };

    const entry = streams.get(sessionId);
    if (entry) entry.eventSource = es;
    else streams.set(sessionId, { eventSource: es, state });
  }

  function disconnectStream(sessionId) {
    const entry = streams.get(sessionId);
    if (!entry) return;
    clearDeadAirTimer(entry.state);
    clearStreamTimer(entry.state);
    stopAllToolCardTimers();
    if (entry.eventSource) {
      entry.eventSource.close();
    }
    streams.delete(sessionId);
  }

  function disconnectAll() {
    for (const [id] of streams) {
      disconnectStream(id);
    }
  }

  function markStreamResumed(state) {
    clearDeadAirTimer(state);
    state.firstEventSeen = true;
    state.awaitingResume = false;
  }

  function handleSSEPacket(sessionId, packet, state) {
    switch (packet.type) {
      case 'connecting':
        state.status = STATES.CONNECTING;
        updateRunStatus(STATES.CONNECTING, defaultStatusDetail(STATES.CONNECTING, state), state);
        armDeadAirTimer(state);
        break;

      case 'thinking_delta':
        markStreamResumed(state);
        state.status = STATES.STREAMING;
        updateRunStatus(STATES.STREAMING, defaultStatusDetail(STATES.STREAMING, state), state);
        appendThinkingDelta(packet.content);
        break;

      case 'token':
        markStreamResumed(state);
        state.status = STATES.STREAMING;
        showStreamingBubble();
        updateRunStatus(STATES.STREAMING, defaultStatusDetail(STATES.STREAMING, state), state);
        appendToken(state, packet.content);
        break;

      case 'tool_call':
        markStreamResumed(state);
        state.status = STATES.TOOL_RUNNING;
        updateRunStatus(STATES.TOOL_RUNNING, 'Running tool: ' + (packet.tool || 'unknown tool'), state);

        // Track tool call key for card slot (monotonic counter for rapid events)
        toolEntryCounter++;
        var toolCallKey = sessionId + '-tool-' + Date.now() + '-' + toolEntryCounter;

        // Skip tool card for render_quick_replies — the actual quick reply chips
        // appear inline on the next assistant message (via InlineQuickReplies).
        // Showing a tool card with "Rendered QuickReplies with options: …" is noise.
        if (packet.tool === 'render_quick_replies') {
          // Ensure streaming bubble exists for whatever follows
          showStreamingBubble();
          break;
        }

        // Inject running tool card into sidebar (issue #320)
        injectToolCardSlot(sessionId, packet, toolCallKey);
        break;

      case 'tool_result':
        markStreamResumed(state);
        state.status = STATES.STREAMING;
        updateRunStatus(STATES.STREAMING, 'Tool finished. Continuing response.', state);

        // Skip tool card render for render_quick_replies (see tool_call above)
        if (packet.tool === 'render_quick_replies') {
          break;
        }

        renderToolCard(sessionId, 'tool_result', packet);
        break;

      case 'context_update':
        markStreamResumed(state);
        state.status = STATES.STREAMING;
        updateRunStatus(STATES.STREAMING, 'Processing context.', state);
        if (typeof window.dispatchContextUpdate === 'function') {
          window.dispatchContextUpdate(packet.data);
        }
        break;

      case 'component':
        markStreamResumed(state);
        renderComponent(sessionId, packet);
        break;

      case 'done':
        clearDeadAirTimer(state);
        state.status = STATES.RENDERING;
        updateRunStatus(STATES.RENDERING, defaultStatusDetail(STATES.RENDERING, state), state);
        showStreamingBubble();
        finalizeMessage(sessionId, packet.message_id, packet.usage, function () {
          state.status = STATES.DONE;
          updateRunStatus(STATES.DONE, defaultStatusDetail(STATES.DONE, state), state);
          disconnectStream(sessionId);
          reenableComposer();
        });
        break;

      case 'needs_confirmation':
        markStreamResumed(state);
        state.status = STATES.STREAMING;
        updateRunStatus(STATES.STREAMING, 'Awaiting user confirmation.', state);
        var path = packet.data && packet.data.path;
        var msg = packet.data && packet.data.message;
        if (!path) path = packet.content || '';
        if (!msg) msg = packet.content || '';
        showConfirmationModal(sessionId, path, msg);
        break;

      case 'error':
        if (typeof window.resetContextPanel === 'function') {
          window.resetContextPanel();
        }
        clearDeadAirTimer(state);
        state.status = STATES.ERROR;
        updateRunStatus(STATES.ERROR, packet.message || defaultStatusDetail(STATES.ERROR, state), state);
        renderError(sessionId, packet.message);
        disconnectStream(sessionId);
        reenableComposer();
        break;

      case 'closed':
        if (typeof window.resetContextPanel === 'function') {
          window.resetContextPanel();
        }
        clearDeadAirTimer(state);
        updateRunStatus(STATES.IDLE, packet.message || 'Session closed.', state);
        disconnectStream(sessionId);
        break;
    }
  }

  function appendToken(state, content) {
    state.streamBuf += content;

    if (content.indexOf('\n') !== -1) {
      flushStreamBuffer(state);
      return;
    }

    if (!state.streamTimer) {
      state.streamTimer = window.setTimeout(function () {
        flushStreamBuffer(state);
      }, FLUSH_INTERVAL);
    }
  }

  function flushStreamBuffer(state) {
    clearStreamTimer(state);
    if (!state.streamBuf) return;

    const text = state.streamBuf;
    state.streamBuf = '';

    const el = document.getElementById('streaming');
    if (!el) return;

    const contentEl = el.querySelector('.message-content') || el;
    const span = document.createElement('span');
    span.textContent = text;
    contentEl.appendChild(span);
  }

  function showStreamingBubble() {
    const messages = document.getElementById('messages');
    if (!messages) return;

    var sentinel = document.getElementById('scroll-sentinel');

    // If HTMX appended elements after sentinel (beforeend swap puts them
    // past the scroll-sentinel), relocate them before sentinel while
    // preserving correct chat ordering: user bubbles go before streaming,
    // assistant/tool elements stay after streaming.
    if (sentinel && sentinel.parentNode === messages) {
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
      el.innerHTML = '<div class="message-avatar">E</div><div class="message-content"></div>';
    }
  }

  function injectToolCardSlot(sessionId, packet, toolCallKey) {
    var list = document.querySelector('#tool-activity .tool-activity-list');
    if (!list) return;

    var toolName = packet.tool || packet.name || 'tool';

    // Idempotent: skip if already exists (e.g. SSE reconnect)
    if (list.querySelector('[data-tool-key="' + toolCallKey + '"]')) return;

    // Max 6 entries — FIFO eviction
    var existingWrappers = list.querySelectorAll('.tool-entry-wrapper');
    while (existingWrappers.length >= 6) {
      var firstKey = existingWrappers[0].getAttribute('data-tool-key');
      if (firstKey) {
        stopToolCardTimer(firstKey);
        delete toolCardElapsed[firstKey];
      }
      existingWrappers[0].remove();
      existingWrappers = list.querySelectorAll('.tool-entry-wrapper');
    }

    var wrapper = document.createElement('div');
    wrapper.className = 'tool-entry-wrapper';
    wrapper.id = toolCallKey;
    wrapper.setAttribute('data-tool-key', toolCallKey);
    // Build compact running row (client-side, no server render)
    wrapper.innerHTML = '<div class="tool-entry tool-running">' +
      '<span class="tool-icon">\uD83D\uDD27</span>' +
      '<span class="tool-name">' + escapeHtml(toolName) + '</span>' +
      '<span class="tool-status-label">running...</span>' +
      '<span class="tool-elapsed" data-tool-elapsed="' + toolCallKey + '"></span>' +
      '</div>';

    list.appendChild(wrapper);

    // Start live elapsed timer
    var startMs = Date.now();
    toolCardElapsed[toolCallKey] = { startMs: startMs, finalMs: null };
    startToolCardTimer(toolCallKey);
  }

  function findToolCardSlot(toolCallKey) {
    return document.querySelector('#tool-activity [data-tool-key="' + toolCallKey + '"]');
  }

  function renderToolCard(sessionId, type, packet) {
    // Find the latest running tool entry in sidebar
    var toolCallKey = '';
    var runningEntry = document.querySelector('#tool-activity .tool-entry.tool-running');
    if (runningEntry) {
      var parentWrapper = runningEntry.closest('.tool-entry-wrapper');
      if (parentWrapper) toolCallKey = parentWrapper.getAttribute('data-tool-key') || '';
    }
    if (!toolCallKey) {
      // Fallback: use latest wrapper
      var allWrappers = document.querySelectorAll('#tool-activity .tool-entry-wrapper');
      if (allWrappers.length > 0) {
        toolCallKey = allWrappers[allWrappers.length - 1].getAttribute('data-tool-key') || '';
      }
    }
    if (!toolCallKey) return;

    // Stop live timer and record final elapsed
    stopToolCardTimer(toolCallKey);
    var finalElapsed = '';
    if (toolCardElapsed[toolCallKey] && toolCardElapsed[toolCallKey].startMs) {
      var elapsedMs = Date.now() - toolCardElapsed[toolCallKey].startMs;
      toolCardElapsed[toolCallKey].finalMs = elapsedMs;
      finalElapsed = formatTimer(elapsedMs);
    }

    // Detect tool error from output
    var output = packet.output || '';
    var isError = typeof output === 'string' && output.indexOf('Tool error:') === 0;

    // Build form data for render endpoint
    const formData = new FormData();
    formData.append('type', type);
    formData.append('tool', packet.tool || packet.name || '');
    formData.append('tool_call_key', toolCallKey);
    formData.append('status', 'done');
    formData.append('elapsed', finalElapsed);
    if (packet.args) formData.append('args', JSON.stringify(packet.args));
    if (output) formData.append('output', String(output));
    if (packet.Args) formData.append('args', JSON.stringify(packet.Args));

    // Find the wrapper in sidebar
    var wrapper = document.querySelector('#tool-activity .tool-entry-wrapper[data-tool-key="' + toolCallKey + '"]');
    if (!wrapper) return;

    // Update entry to done/error state before HTMX replaces it
    var entry = wrapper.querySelector('.tool-entry');
    if (entry) {
      entry.className = 'tool-entry ' + (isError ? 'tool-error' : 'tool-done');
      var icon = entry.querySelector('.tool-icon');
      if (icon) icon.textContent = isError ? '\u274C' : '\u2705';
      var label = entry.querySelector('.tool-status-label');
      if (label) label.textContent = isError ? 'error' : 'done';
      var elapsedSpan = entry.querySelector('.tool-elapsed');
      if (elapsedSpan && finalElapsed) elapsedSpan.textContent = '\u2191 ' + finalElapsed;
    }

    // Attach click-to-expand on entry
    if (entry) {
      entry.addEventListener('click', function (e) {
        e.stopPropagation();
        var outputDiv = wrapper.querySelector('.tool-output');
        if (!outputDiv) return;
        // Close all other open outputs (only one at a time)
        document.querySelectorAll('#tool-activity .tool-output.open').forEach(function (el) {
          if (el !== outputDiv) el.classList.remove('open');
        });
        // Lazy-load output on first click via HTMX
        if (!outputDiv.dataset.rendered) {
          outputDiv.dataset.rendered = '1';
          const jsonPayload = Object.assign(
            Object.fromEntries(formData),
            { kind: 'tool_card' }
          );
          htmx.ajax('POST', '/api/sessions/' + sessionId + '/render', {
            source: document.body,
            target: '#' + CSS.escape(outputDiv.id),
            swap: 'innerHTML',
            contentType: 'application/json',
            values: jsonPayload,
          });
        }
        outputDiv.classList.toggle('open');
        // Post-open: update error icon if server rendered error content
        if (isError) {
          var serverIcon = outputDiv.querySelector('.tool-icon');
          if (serverIcon) serverIcon.textContent = '\u274C';
        }
      });
    }

    // Create output container for HTMX render (hidden until clicked)
    var outputId = toolCallKey + '-output';
    var existingOutput = wrapper.querySelector('.tool-output');
    if (!existingOutput) {
      var outputDiv = document.createElement('div');
      outputDiv.className = 'tool-output';
      outputDiv.id = outputId;
      outputDiv.setAttribute('data-output-for', toolCallKey);
      wrapper.appendChild(outputDiv);
    }
  }

  function renderComponent(sessionId, packet) {
    console.log('[eitri] renderComponent called', JSON.stringify(packet));
    // The SSE 'component' event nests name/data inside packet.data:
    //   {"type":"component","data":{"name":"FileEditCard","data":{...}}}
    var nested = packet.data || {};
    const compName = nested.name || '';
    const compData = nested.data || {};
    if (!compName) {
      console.warn('[eitri] renderComponent: no compName, packet.data=', JSON.stringify(packet.data));
      return;
    }
    console.log('[eitri] renderComponent: name=' + compName + ' data keys=' + Object.keys(compData).join(','));

    if (compName === 'FileEditCard') {
      var doRender = function () {
        // Find the outermost tool-entry-wrapper in sidebar
        var allWrappers = document.querySelectorAll('#tool-activity .tool-entry-wrapper');
        if (allWrappers.length > 0) {
          var wrapper = allWrappers[allWrappers.length - 1];
          htmx.ajax('POST', '/api/sessions/' + sessionId + '/render', {
            source: document.body,
            target: '#' + CSS.escape(wrapper.id),
            swap: 'innerHTML',
            contentType: 'application/json',
            values: {
              kind: 'component',
              name: compName,
              data: compData,
            },
          });
        }
      };
      // First try on next tick (after tool_result HTMX settles).
      // Then retry once after a short delay in case HTMX hasn't finished.
      setTimeout(doRender, 0);
      setTimeout(doRender, 100);
      return;
    }

    if (compName === 'MermaidDiagram') {
      return;
    }

    // Insert other visual components after the streaming bubble so they
    // visually group with the LLM response.
    var streaming = document.getElementById('streaming');
    if (!streaming) {
      console.warn('[eitri] renderComponent: no #streaming element');
      return;
    }
    console.log('[eitri] renderComponent: inserting after #streaming');

    htmx.ajax('POST', '/api/sessions/' + sessionId + '/render', {
      source: document.body,
      target: '#streaming',
      swap: 'afterend',
      contentType: 'application/json',
      values: {
        kind: 'component',
        name: compName,
        data: compData,
      },
    });
  }

  function finalizeMessage(sessionId, messageId, usage, onRendered) {
    const streamingEl = document.getElementById('streaming');
    if (streamingEl) {
      streamingEl.style.opacity = '0.5';
      streamingEl.classList.add('rendering');
    }

    let completed = false;
    function finish() {
      if (completed) return;
      completed = true;
      document.body.removeEventListener('htmx:afterSwap', afterSwap);
      appendTokenUsage(usage);
      if (typeof onRendered === 'function') onRendered();
    }

    function afterSwap(evt) {
      const target = evt.detail && evt.detail.target;
      if (target && target.id === 'streaming') {
        finish();
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

  function appendThinkingDelta(content) {
    var el = document.querySelector('#thinking-panel .thinking-content');
    if (!el) return;
    el.textContent += content;
    // Auto-scroll to bottom as content arrives
    el.scrollTop = el.scrollHeight;
  }

  // ---- Live elapsed timer for tool cards (issue #134) ----

  function startToolCardTimer(toolCallKey) {
    stopToolCardTimer(toolCallKey); // Ensure no duplicate timers
    toolCardTimers[toolCallKey] = window.setInterval(function () {
      var elapsedSpan = document.querySelector('[data-tool-elapsed="' + toolCallKey + '"]');
      if (!elapsedSpan) return;
      var elapsed = toolCardElapsed[toolCallKey];
      if (!elapsed || !elapsed.startMs) return;
      var diff = Date.now() - elapsed.startMs;
      elapsedSpan.textContent = '\u2191 ' + formatTimer(diff);
    }, 200);
  }

  function stopToolCardTimer(toolCallKey) {
    if (toolCardTimers[toolCallKey]) {
      window.clearInterval(toolCardTimers[toolCallKey]);
      delete toolCardTimers[toolCallKey];
    }
  }

  function stopAllToolCardTimers() {
    for (var key in toolCardTimers) {
      if (toolCardTimers.hasOwnProperty(key)) {
        stopToolCardTimer(key);
      }
    }
  }

  // ---- Confirmation modal for blocked read paths (issue #314) ----

  var activeConfirmation = null; // { sessionId, path, message }

  function showConfirmationModal(sessionId, path, message) {
    closeConfirmationModal();

    activeConfirmation = { sessionId: sessionId, path: path, message: message };

    var overlay = document.createElement('div');
    overlay.id = 'confirmation-overlay';
    overlay.className = 'confirmation-overlay';

    overlay.innerHTML =
      '<div class="confirmation-modal" role="dialog" aria-modal="true" aria-labelledby="confirmation-title">' +
      '<h3 id="confirmation-title">&#9888; Path requires confirmation</h3>' +
      '<div class="confirmation-path">' + escapeHtml(path) + '</div>' +
      '<div class="confirmation-message">' + escapeHtml(message) + '</div>' +
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

    // Keyboard: Enter on Allow button, Escape not allowed (must decide)
    document.addEventListener('keydown', confirmationKeyHandler);
  }

  function closeConfirmationModal() {
    var overlay = document.getElementById('confirmation-overlay');
    if (overlay) {
      overlay.remove();
    }
    document.removeEventListener('keydown', confirmationKeyHandler);
    activeConfirmation = null;
  }

  function confirmationKeyHandler(e) {
    if (e.key === 'Enter') {
      var allowBtn = document.getElementById('confirm-allow');
      if (allowBtn && document.activeElement === allowBtn) {
        resolveConfirmation(true);
      }
      var denyBtn = document.getElementById('confirm-deny');
      if (denyBtn && document.activeElement === denyBtn) {
        resolveConfirmation(false);
      }
    }
  }

  function resolveConfirmation(approved) {
    if (!activeConfirmation) return;

    var allowBtn = document.getElementById('confirm-allow');
    var denyBtn = document.getElementById('confirm-deny');
    if (allowBtn) allowBtn.disabled = true;
    if (denyBtn) denyBtn.disabled = true;

    var sessionId = activeConfirmation.sessionId;
    var path = activeConfirmation.path;

    fetch('/api/sessions/' + encodeURIComponent(sessionId) + '/confirm', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: path, approved: approved }),
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
  }

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

  function reinitScrollObserver() {
    var sentinel = document.getElementById('scroll-sentinel');
    if (!sentinel) return;

    // Disconnect old observer if any
    if (sentinel._scrollObserver) {
      sentinel._scrollObserver.disconnect();
    }

    initScrollToBottomButton();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () {
      ensureChatChrome();
      initCodeBlockButtons();
      initScrollToBottomButton();
    });
  } else {
    ensureChatChrome();
    initCodeBlockButtons();
    initScrollToBottomButton();
  }

  document.addEventListener('htmx:afterSwap', function () {
    ensureChatChrome();
    initCodeBlockButtons();
    reinitScrollObserver();
  });
  document.addEventListener('htmx:afterSettle', initCodeBlockButtons);

  // ---- Optimistic user bubble and auto-scroll (issue #95) ----

  function insertOptimisticBubble(text) {
    const messages = document.getElementById('messages');
    if (!messages || !text) return;
    if (messages.querySelector('[data-optimistic="true"]')) return;
    const bubble = document.createElement('div');
    bubble.className = 'message message-user';
    bubble.setAttribute('data-optimistic', 'true');
    bubble.innerHTML = '<div class="message-avatar">U</div><div class="message-content">' + escapeHtml(text) + '</div>';
    messages.appendChild(bubble);
  }

  function escapeHtml(str) {
    var div = document.createElement('div');
    div.appendChild(document.createTextNode(str));
    return div.innerHTML;
  }

  function removeOptimisticBubbles() {
    var bubbles = document.querySelectorAll('[data-optimistic="true"]');
    for (var i = 0; i < bubbles.length; i++) {
      bubbles[i].remove();
    }
  }

  function scrollToLatest() {
    var messages = document.getElementById('messages');
    if (!messages) return;
    var lastChild = messages.lastElementChild;
    if (lastChild) {
      lastChild.scrollIntoView({ behavior: 'smooth', block: 'end' });
    }
  }

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
      removeOptimisticBubbles();
      setTimeout(scrollToLatest, 50);
    }
  });

  // Wrap appendToken for auto-scroll
  var _origAppendToken = appendToken;
  appendToken = function (state, content) {
    _origAppendToken(state, content);
    setTimeout(scrollToLatest, 20);
  };

  // Wrap showStreamingBubble for auto-scroll
  var _origShowStreamingBubble = showStreamingBubble;
  showStreamingBubble = function () {
    _origShowStreamingBubble();
    setTimeout(scrollToLatest, 20);
  };

  // Wrap finalizeMessage for auto-scroll
  var _origFinalizeMessage = finalizeMessage;
  finalizeMessage = function (sessionId, messageId, usage, onRendered) {
    _origFinalizeMessage(sessionId, messageId, usage, function () {
      if (typeof onRendered === 'function') onRendered();
      setTimeout(scrollToLatest, 100);
    });
  };

})();