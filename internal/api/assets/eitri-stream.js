// eitri-stream — Browser island for managing SSE stream lifecycle.
// Handles EventSource connection, token display, tool cards, activity panel, and render dispatch.

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

  var activityToolCount = 0;
  var activityToolSummary = []; // array of {label, brief} for summary display
  var activityElapsed = {}; // toolCallKey -> start time (Date.now)
  var toolCardTimers = {}; // toolCallKey -> interval ID
  var toolCardElapsed = {}; // toolCardKey -> {startMs, finalMs}

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
    activityToolCount = 0;
    activityToolSummary = [];
    activityElapsed = {};
    toolCardTimers = {};
    toolCardElapsed = {};
  }

  function autoOpenActivityPanel() {
    var panel = document.getElementById('activity-panel');
    if (panel && !panel.open) {
      panel.open = true;
    }
  }

  function updateActivitySummary() {
    var summary = document.querySelector('#activity-panel summary');
    if (!summary) return;

    var previewHtml = '';
    if (activityToolCount > 0) {
      // Build a compact preview: tool name + brief summary for up to 3 items
      var previews = [];
      for (var i = 0; i < activityToolSummary.length && i < 3; i++) {
        var s = activityToolSummary[i];
        previews.push(s.brief || s.label);
      }
      if (activityToolSummary.length > 3) {
        previews.push('…');
      }
      var preview = escapeHtml(previews.join(', '));
      previewHtml = ' (' + activityToolCount + '): <span class="activity-summary-preview">' + preview + '</span>';
    }

    summary.innerHTML = '<span>Activity' + previewHtml + '</span><span id="activity-count" class="activity-count">0</span>';

    // Restore count from activity-log entries so updateActivitySummary and updateActivityCount are consistent
    var log = document.getElementById('activity-log');
    if (log) {
      var countEl = document.getElementById('activity-count');
      if (countEl) {
        countEl.textContent = String(log.querySelectorAll('.activity-entry').length);
      }
    }
  }

  function activityBriefForPacket(packet) {
    if (packet.tool === 'terminal_execute' && packet.args && typeof packet.args.command === 'string') {
      var cmd = packet.args.command;
      return cmd.length > 40 ? cmd.slice(0, 40) + '…' : cmd;
    }
    if (packet.tool === 'file_editor' && packet.args && typeof packet.args.path === 'string') {
      return packet.args.path;
    }
    return packet.tool || '';
  }

  function formatElapsed(ms) {
    if (ms < 1000) return ms + 'ms';
    if (ms < 60000) return (ms / 1000).toFixed(1) + 's';
    return Math.floor(ms / 60000) + 'm ' + Math.floor((ms % 60000) / 1000) + 's';
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
    updateActivityCount();
    updateActivitySummary();
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

  function resetActivityPanel() {
    const log = document.getElementById('activity-log');
    const panel = document.getElementById('activity-panel');
    if (!log) return;
    log.replaceChildren();
    const empty = document.createElement('p');
    empty.id = 'activity-empty';
    empty.className = 'text-muted';
    empty.textContent = 'No tool activity yet.';
    log.appendChild(empty);
    if (panel) panel.open = false;
    stopAllToolCardTimers();
    resetActivityTracking();
    updateActivityCount();
    updateActivitySummary();
  }

  function updateActivityCount() {
    const countEl = document.getElementById('activity-count');
    const log = document.getElementById('activity-log');
    if (!countEl || !log) return;
    const count = log.querySelectorAll('.activity-entry').length;
    countEl.textContent = String(count);
  }

  function appendActivityEntry(label, detail, meta, toolKey) {
    const log = document.getElementById('activity-log');
    if (!log) return;

    const empty = document.getElementById('activity-empty');
    if (empty) empty.remove();

    const entry = document.createElement('div');
    entry.className = 'activity-entry';

    const header = document.createElement('div');
    header.className = 'activity-entry-header';

    const labelEl = document.createElement('span');
    labelEl.className = 'activity-entry-label';
    labelEl.textContent = label;
    header.appendChild(labelEl);

    // Show elapsed time in meta for finished tools
    if (meta) {
      var metaText = meta;
      if (toolKey && activityElapsed[toolKey]) {
        var elapsed = Date.now() - activityElapsed[toolKey];
        metaText = meta + ' (' + formatElapsed(elapsed) + ')';
      }
      const metaEl = document.createElement('span');
      metaEl.className = 'activity-entry-meta';
      metaEl.textContent = metaText;
      header.appendChild(metaEl);
    } else if (toolKey && activityElapsed[toolKey]) {
      var elapsed2 = Date.now() - activityElapsed[toolKey];
      const metaEl = document.createElement('span');
      metaEl.className = 'activity-entry-meta';
      metaEl.textContent = formatElapsed(elapsed2);
      header.appendChild(metaEl);
    }

    entry.appendChild(header);

    if (detail) {
      var detailText = detail;
      var truncated = false;
      if (detailText.length > 300) {
        detailText = detailText.slice(0, 300) + '…';
        truncated = true;
      }
      var detailEl = document.createElement('div');
      detailEl.className = 'activity-entry-detail';
      detailEl.textContent = detailText;
      entry.appendChild(detailEl);

      if (truncated) {
        var expandBtn = document.createElement('button');
        expandBtn.className = 'activity-expand-btn';
        expandBtn.textContent = 'Show full output';
        expandBtn.addEventListener('click', function () {
          if (detailEl.textContent === detailText) {
            detailEl.textContent = detail;
            expandBtn.textContent = 'Show less';
          } else {
            detailEl.textContent = detailText;
            expandBtn.textContent = 'Show full output';
          }
        });
        entry.appendChild(expandBtn);
      }
    }

    log.appendChild(entry);
    updateActivityCount();
  }

  function summarizeToolDetail(packet) {
    if (packet.tool === 'terminal_execute' && packet.args && typeof packet.args.command === 'string') {
      return packet.args.command;
    }
    if (packet.tool === 'file_editor' && packet.args && typeof packet.args.path === 'string') {
      return packet.args.path;
    }
    if (typeof packet.output === 'string' && packet.output) {
      return packet.output.length > 120 ? packet.output.slice(0, 120) + '…' : packet.output;
    }
    return '';
  }

  document.addEventListener('eitri:connectRunStream', function (e) {
    const sessionId = extractSessionId(e.detail, e.target);
    if (!sessionId) return;
    connectStream(sessionId);
  });

  function reenableComposer() {
    const input = document.getElementById('chat-input');
    const sendBtn = document.getElementById('send-btn');
    const stopBtn = document.getElementById('stop-btn');
    if (input) input.disabled = false;
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
    resetActivityPanel();

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

        // Track for activity summary
        var brief = activityBriefForPacket(packet);
        var toolCallKey = sessionId + '-tool-' + activityToolCount;
        activityToolCount++;
        activityToolSummary.push({ label: packet.tool || 'tool', brief: brief });
        activityElapsed[toolCallKey] = Date.now();

        // Auto-open panel on first tool
        if (activityToolCount === 1) {
          autoOpenActivityPanel();
        }

        appendActivityEntry('Started ' + (packet.tool || 'tool'), summarizeToolDetail(packet), 'running', toolCallKey);
        updateActivitySummary();

        // Inject running tool card into message stream (issue #130)
        // Create slot and set running card HTML directly (synchronous, no HTMX race with tool_result)
        injectToolCardSlot(sessionId, packet, toolCallKey);
        break;

      case 'tool_result':
        markStreamResumed(state);
        state.status = STATES.STREAMING;
        updateRunStatus(STATES.STREAMING, 'Tool finished. Continuing response.', state);

        // Find tool call key for elapsed tracking
        // Use the packet's position or sequential tracking
        var resultKey = sessionId + '-tool-' + (activityToolCount - 1);
        // If we have multiple concurrent, find matching
        if (!activityElapsed[resultKey]) {
          resultKey = '';
        }

        appendActivityEntry('Finished ' + (packet.tool || 'tool'), summarizeToolDetail(packet), 'done', resultKey);
        renderToolCard(sessionId, 'tool_result', packet);
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

      case 'error':
        clearDeadAirTimer(state);
        state.status = STATES.ERROR;
        updateRunStatus(STATES.ERROR, packet.message || defaultStatusDetail(STATES.ERROR, state), state);
        renderError(sessionId, packet.message);
        disconnectStream(sessionId);
        reenableComposer();
        break;

      case 'closed':
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

    let el = document.getElementById('streaming');
    if (!el) {
      el = document.createElement('div');
      el.id = 'streaming';
      messages.appendChild(el);
    }

    // Move streaming (and scroll-sentinel) to end of #messages so response
    // appears below any HTMX-appended user bubbles. Fix: first-message
    // response rendered above user message because server-rendered streaming
    // div precedes the HTMX-appended user bubble.
    if (messages.lastElementChild !== el) {
      var sentinel = document.getElementById('scroll-sentinel');
      // Move streaming first, then sentinel — sentinel stays as absolute last child
      messages.appendChild(el);
      if (sentinel) messages.appendChild(sentinel);
    }

    if (!el.classList.contains('message-assistant')) {
      el.className = 'message message-assistant streaming-message';
      el.innerHTML = '<div class="message-avatar">E</div><div class="message-content"></div>';
    }
  }

  function injectToolCardSlot(sessionId, packet, toolCallKey) {
    const messages = document.getElementById('messages');
    if (!messages) return;

    var toolName = packet.tool || packet.name || 'tool';
    var argsStr = packet.args ? JSON.stringify(packet.args, null, 2) : '';

    var container = getOrCreateToolContainer(sessionId, messages);
    if (!container) return;

    // Idempotent: skip if slot already exists (e.g. SSE reconnect)
    if (container.querySelector('[data-tool-id="' + toolCallKey + '"]')) return;

    var slot = document.createElement('div');
    slot.className = 'tool-call-container';
    slot.setAttribute('data-tool-id', toolCallKey);
    container.appendChild(slot);

    // Build running card HTML (mirrors server-rendered ToolCard with status=running)
    // Include an elapsed span that the live timer will update (issue #134)
    var html = '<div class="tool-card tool-running" data-tool-id="' + toolCallKey + '">' +
      '<div class="tool-card-header">' +
      '<span class="tool-icon">\uD83D\uDD27</span>' +
      '<span class="tool-name">' + escapeHtml(toolName) + '</span>' +
      '<span class="tool-status">running...</span>' +
      '<span class="tool-elapsed" data-tool-elapsed="' + toolCallKey + '"></span>' +
      '</div>' +
      (argsStr ? '<pre class="tool-args"><code>' + escapeHtml(argsStr) + '</code></pre>' : '') +
      '</div>';
    slot.innerHTML = html;

    // Start live elapsed timer (issue #134)
    var startMs = Date.now();
    toolCardElapsed[toolCallKey] = { startMs: startMs, finalMs: null };
    startToolCardTimer(toolCallKey);
  }

  function getOrCreateToolContainer(sessionId, messages) {
    const containerId = 'tool-cards-' + sessionId;
    let container = document.getElementById(containerId);
    if (!container) {
      container = document.createElement('div');
      container.id = containerId;
      container.className = 'tool-cards-container';
      var sentinel = document.getElementById('scroll-sentinel');
      if (sentinel && sentinel.parentNode === messages) {
        messages.insertBefore(container, sentinel);
      } else {
        messages.appendChild(container);
      }
    }
    return container;
  }

  function renderToolCard(sessionId, type, packet) {
    const messages = document.getElementById('messages');
    if (!messages) return;

    // Compute toolCallKey matching activity panel tracking
    var toolCallKey = sessionId + '-tool-' + (activityToolCount - 1);

    // Stop live timer and record final elapsed (issue #134)
    stopToolCardTimer(toolCallKey);
    var finalElapsed = '';
    if (toolCardElapsed[toolCallKey] && toolCardElapsed[toolCallKey].startMs) {
      var elapsedMs = Date.now() - toolCardElapsed[toolCallKey].startMs;
      toolCardElapsed[toolCallKey].finalMs = elapsedMs;
      finalElapsed = formatTimer(elapsedMs);
    }

    const formData = new FormData();
    formData.append('type', type);
    formData.append('tool', packet.tool || packet.name || '');
    formData.append('tool_call_key', toolCallKey);
    formData.append('status', 'done');
    formData.append('elapsed', finalElapsed);
    if (packet.args) formData.append('args', JSON.stringify(packet.args));
    if (packet.output) formData.append('output', String(packet.output));
    if (packet.Args) formData.append('args', JSON.stringify(packet.Args));

    var container = getOrCreateToolContainer(sessionId, messages);
    if (!container) return;

    // Find existing slot created by injectToolCardSlot
    var slot = container.querySelector('[data-tool-id="' + toolCallKey + '"]');
    if (!slot) {
      // Fallback: create slot if injectToolCardSlot missed (shouldn't happen)
      slot = document.createElement('div');
      slot.className = 'tool-call-container';
      slot.setAttribute('data-tool-id', toolCallKey);
      container.appendChild(slot);
    }

    // POST to unified render route with kind field (issue #195)
    const jsonPayload = Object.assign(
      Object.fromEntries(formData),
      { kind: 'tool_card' }
    );
    htmx.ajax('POST', '/api/sessions/' + sessionId + '/render', {
      source: document.body,
      target: '#' + CSS.escape(slot.id || (slot.id = toolCallKey)),
      swap: 'innerHTML',
      contentType: 'application/json',
      values: JSON.stringify(jsonPayload),
    });
  }

  function renderComponent(sessionId, packet) {
    const messages = document.getElementById('messages');
    if (!messages) return;

    const compName = packet.name || '';
    const compData = packet.data || {};
    if (!compName) return;

    htmx.ajax('POST', '/api/sessions/' + sessionId + '/render', {
      source: document.body,
      target: '#messages',
      swap: 'beforeend',
      contentType: 'application/json',
      values: JSON.stringify({
        kind: 'component',
        name: compName,
        data: compData,
      }),
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
      values: JSON.stringify({
        kind: 'markdown',
        message_id: messageId || '',
      }),
    });

    window.setTimeout(finish, 500);
  }

  function renderError(sessionId, message) {
    htmx.ajax('POST', '/api/sessions/' + sessionId + '/render', {
      source: document.body,
      target: '#error-toasts',
      swap: 'beforeend',
      contentType: 'application/json',
      values: JSON.stringify({
        kind: 'error',
        message: message || 'An error occurred',
      }),
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