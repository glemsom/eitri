// eitri-stream-common — Shared stream lifecycle runtime for the browser island.
// Defines the single `window.eitriStream` namespace every stream module reads
// and writes, plus the pure, stateless helpers (markdown, status labels,
// timers, session-id extraction) and the per-session stream state shape.
// Loaded first, before any sibling stream module, so the runtime exists when
// the other modules bootstrap. Mirrors the shared-runtime `window.*` pattern
// eitri-session-id.js uses for cross-island helpers.
(function () {
  'use strict';

  window.eitriStream = window.eitriStream || {};
  var S = window.eitriStream;

  S.STATES = {
    IDLE: 'idle',
    CONNECTING: 'connecting',
    STREAMING: 'streaming',
    TOOL_RUNNING: 'tool-running',
    RENDERING: 'rendering',
    DONE: 'done',
    ERROR: 'error',
    RECONNECTING: 'reconnecting',
  };

  S.FLUSH_INTERVAL = 80;
  S.NO_DEAD_AIR_MS = 650;
  // Screen-reader announcement pacing for streaming replies (issue #1071).
  // The visible streaming bubble is re-rendered every ~80ms flush, so marking
  // IT as a live region would make assistive tech re-read the whole reply at
  // token cadence. Instead a visually-hidden role="status" region receives
  // only *new* text, at most once per ANNOUNCE_INTERVAL_MS, so screen readers
  // announce the reply in chunks without ever re-reading the full stream.
  S.ANNOUNCE_INTERVAL_MS = 1000;
  // Armed the moment an HTMX swap touches #messages, so showStreamingBubble knows
  // there may be elements past the scroll-sentinel to relocate. The relocation
  // walk is O(history) and ran on every streaming flush before; gating it on this
  // flag keeps it to once per actual swap instead of once per token.
  S.relocatePending = false;
  // A single growing block longer than this many chars is streamed append-only
  // as raw (escaped) text instead of re-rendered as markdown each flush — see
  // flushStreamBuffer. Re-rendering a huge in-progress block from scratch is
  // O(total) per flush → O(n²) over the stream and freezes the main thread.
  S.STREAM_TAIL_RAW_LIMIT = 16384;

  // sessionId -> { eventSource, state } for every live EventSource.
  S.streams = new Map();
  // Guard against reconnect storms after no_active_run: sessionId -> last
  // `no_active_run` timestamp.
  S.noActiveRunTimestamps = {};

  // Tool-card shared state (issue #1070): keys are replay-stable toolCallKeys.
  S.toolCardTimers = {}; // toolCallKey -> interval ID
  S.toolCardElapsed = {}; // toolCardKey -> {startMs, finalMs}
  S.toolArgs = {}; // toolCallKey -> args JSON
  S.toolNames = {}; // toolCallKey -> tool name
  S.toolEntryCounter = 0; // monotonic counter for unique tool keys

  // Thinking-panel + auto-scroll coalescing flags (per-island, single page).
  S.thinkingScrollPending = false;
  S.thinkingPanelMaxText = 20000;
  S.autoScrollPending = false;

  // Confirmation modal state (issue #314 / #1067).
  S.activeConfirmation = null; // { sessionId, path, message }
  S.lastFocusedElement = null; // element to restore focus to on close

  function escapeHtml(str) {
    var div = document.createElement('div');
    div.appendChild(document.createTextNode(str));
    return div.innerHTML;
  }
  S.escapeHtml = escapeHtml;

  function lightweightMarkdown(text) {
    // 1. HTML escape
    var safe = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

    // 2. Bold: **text**
    safe = safe.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');

    // 3. Italic: *text* (non-greedy, after bold so bold ** doesn't match)
    safe = safe.replace(/\*(.+?)\*/g, '<em>$1</em>');

    // 4. Inline code: \`code\`
    safe = safe.replace(/`([^`]+)`/g, '<code>$1</code>');

    // 5. Links: [text](url) — only http://, https://, mailto: allowed
    safe = safe.replace(/\[([^\]]+)\]\(([^)]+)\)/g, function(match, linkText, url) {
      if (/^(https?:|mailto:)/i.test(url)) {
        return '<a href="' + url + '" target="_blank" rel="noopener">' + linkText + '</a>';
      }
      // Disallowed scheme (javascript:, data:, file:, vbscript:) → plain text
      return match;
    });

    // 6. Lists: split into paragraphs by \n\n, detect list blocks
    var paragraphs = safe.split('\n\n');
    var result = [];

    for (var p = 0; p < paragraphs.length; p++) {
      var para = paragraphs[p].trim();
      if (para === '') continue;

      // Check if all non-empty lines are list items
      var lines = para.split('\n');
      var isList = true;
      var hasTask = false;
      var hasOrdered = false;

      for (var i = 0; i < lines.length; i++) {
        var line = lines[i];
        if (line.trim() === '') continue;
        if (/^- \[[ x]\] /.test(line)) {
          hasTask = true;
        } else if (/^[-*+] /.test(line)) {
          // unordered list item
        } else if (/^\d+\. /.test(line)) {
          hasOrdered = true;
        } else {
          isList = false;
          break;
        }
      }

      if (isList && lines.some(function(l) { return l.trim() !== ''; })) {
        // Render as a list
        if (hasTask) {
          result.push('<ul class="task-list">');
        } else if (hasOrdered) {
          result.push('<ol>');
        } else {
          result.push('<ul>');
        }

        for (var i = 0; i < lines.length; i++) {
          var line = lines[i];
          if (line.trim() === '') continue;

          var content = line;
          var checkbox = '';

          // Task list: - [ ] or - [x]. The checkbox and its text are wrapped
          // in a <label> so screen readers announce "text, checkbox" instead
          // of an unlabelled checkbox (issue #1071).
          var taskMatch = line.match(/^- \[([ x])\] (.+)$/);
          if (taskMatch) {
            checkbox = '<label><input type="checkbox"' + (taskMatch[1] === 'x' ? ' checked=""' : '') + ' disabled="" /> ';
            content = checkbox + taskMatch[2] + '</label>';
          } else if (line.match(/^[-*+] (.+)$/)) {
            content = line.match(/^[-*+] (.+)$/)[1];
          } else if (line.match(/^\d+\. (.+)$/)) {
            content = line.match(/^\d+\. (.+)$/)[1];
          }

          result.push('<li>' + content + '</li>');
        }

        if (hasTask) {
          result.push('</ul>');
        } else if (hasOrdered) {
          result.push('</ol>');
        } else {
          result.push('</ul>');
        }
      } else {
        // Render as paragraph — collapse internal newlines to spaces
        result.push('<p>' + para.replace(/\n/g, ' ') + '</p>');
      }
    }

    safe = result.join('');

    return safe;
  }
  S.lightweightMarkdown = lightweightMarkdown;

  // Format for tool card live timer (issue #134)
  // Sub-second: '0.3s', under 1m: '1.2s', under 1h: '45s', over 1h: '2m 13s'
  function formatTimer(ms) {
    if (ms < 1000) return (ms / 1000).toFixed(1) + 's';
    if (ms < 60000) return (ms / 1000).toFixed(1) + 's';
    return Math.floor(ms / 60000) + 'm ' + Math.floor((ms % 60000) / 1000) + 's';
  }
  S.formatTimer = formatTimer;

  function statusLabel(status) {
    switch (status) {
      case S.STATES.IDLE:
        return 'Idle';
      case S.STATES.CONNECTING:
        return 'Connecting';
      case S.STATES.STREAMING:
        return 'Streaming';
      case S.STATES.TOOL_RUNNING:
        return 'Tool running';
      case S.STATES.RENDERING:
        return 'Rendering';
      case S.STATES.DONE:
        return 'Done';
      case S.STATES.ERROR:
        return 'Error';
      case S.STATES.RECONNECTING:
        return 'Reconnecting';
      default:
        return 'Idle';
    }
  }
  S.statusLabel = statusLabel;

  function defaultStatusDetail(status, state) {
    switch (status) {
      case S.STATES.IDLE:
        return 'Ready for next run.';
      case S.STATES.CONNECTING:
        if (state && !state.firstEventSeen) {
          return 'Connecting to run stream.';
        }
        return 'Waiting for stream to resume.';
      case S.STATES.STREAMING:
        return 'Receiving assistant response.';
      case S.STATES.TOOL_RUNNING:
        return 'Tool activity in progress.';
      case S.STATES.RENDERING:
        return 'Rendering final assistant message.';
      case S.STATES.DONE:
        return 'Run complete.';
      case S.STATES.ERROR:
        return 'Run failed.';
      case S.STATES.RECONNECTING:
        return 'Connection dropped. Waiting to resume stream.';
      default:
        return '';
    }
  }
  S.defaultStatusDetail = defaultStatusDetail;

  function extractSessionId(detail, target) {
    if (typeof detail === 'string') return detail;
    if (detail && typeof detail.value === 'string') return detail.value;
    if (detail && typeof detail.sessionId === 'string') return detail.sessionId;
    if (target && typeof target.value === 'string') return target.value;
    return '';
  }
  S.extractSessionId = extractSessionId;
  function getSessionIdFromUrl() {
    return window.eitriGetSessionId();
  }
  S.getSessionIdFromUrl = getSessionIdFromUrl;

  // Fetch updated active skill chips from the server and OOB-swap them
  function fetchActiveSkillChips(sessionId) {
    htmx.ajax('GET', '/api/sessions/' + sessionId + '/skills/chips', {
      source: document.body,
      target: '#active-skills',
      swap: 'outerHTML',
    });
  }
  S.fetchActiveSkillChips = fetchActiveSkillChips;

  function createStreamState() {
    return {
      status: S.STATES.IDLE,
      firstEventSeen: false,
      awaitingResume: false,
      streamBuf: '',
      renderedBase: '', // length-matched prefix of streamBuf already committed to DOM as stable blocks
      streamTimer: null,
      deadAirTimer: null,
      lastAnnouncedLen: 0, // streamBuf offset already handed to the screen-reader announcer (issue #1071)
      announcePending: '', // new text waiting for the next throttled announcement
      announceTimer: null,
      needsSectionBreak: false,
      lastToolCallKey: '', // set on tool_call, consumed on tool_result and component
      toolKeysByIdentity: {}, // replay-stable (turn+tool+args) -> toolCallKey (issue #1070)
    };
  }
  S.createStreamState = createStreamState;
})();
