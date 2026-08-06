// eitri-stream-toolcards — browser island module for live tool cards.
// Owns the `#tool-activity` sidebar cards: injection on `tool_call`, final
// render on `tool_result`, replay-stable keys (issue #1070), FIFO eviction
// (max 6), and live elapsed timers that die with their cards. Reads/writes the
// shared `window.eitriStream` runtime owned by eitri-stream-common.js.
(function () {
  'use strict';

  var S = window.eitriStream;

  // Stops a card's timer and drops its cached state. Used by FIFO eviction,
  // full teardown, and the timer's own DOM-removal guard (issue #1070).
  function pruneToolCardState(toolCallKey) {
    if (!toolCallKey) return;
    stopToolCardTimer(toolCallKey);
    delete S.toolCardElapsed[toolCallKey];
    delete S.toolArgs[toolCallKey];
    delete S.toolNames[toolCallKey];
  }
  S.pruneToolCardState = pruneToolCardState;

  function startToolCardTimer(toolCallKey) {
    stopToolCardTimer(toolCallKey); // Ensure no duplicate timers
    S.toolCardTimers[toolCallKey] = window.setInterval(function () {
      var elapsedSpan = document.querySelector('[data-tool-elapsed="' + toolCallKey + '"]');
      if (!elapsedSpan || !elapsedSpan.isConnected) {
        // Card left the DOM (sidebar swap, session switch, FIFO eviction) —
        // stop the timer so it cannot leak past its card (issue #1070).
        pruneToolCardState(toolCallKey);
        return;
      }
      var elapsed = S.toolCardElapsed[toolCallKey];
      if (!elapsed || !elapsed.startMs) return;
      var diff = Date.now() - elapsed.startMs;
      elapsedSpan.textContent = '\u2191 ' + S.formatTimer(diff);
    }, 200);
  }
  S.startToolCardTimer = startToolCardTimer;

  function stopToolCardTimer(toolCallKey) {
    if (S.toolCardTimers[toolCallKey]) {
      window.clearInterval(S.toolCardTimers[toolCallKey]);
      delete S.toolCardTimers[toolCallKey];
    }
  }
  S.stopToolCardTimer = stopToolCardTimer;

  function stopAllToolCardTimers() {
    for (var key in S.toolCardTimers) {
      if (S.toolCardTimers.hasOwnProperty(key)) {
        stopToolCardTimer(key);
      }
    }
  }
  S.stopAllToolCardTimers = stopAllToolCardTimers;

  // Test hook: keys of tool cards whose live elapsed interval is still running,
  // so browser E2E tests can assert interval timers die with their cards
  // (issue #1070).
  window.__activeToolCardTimerKeys = function () {
    return Object.keys(S.toolCardTimers);
  };

  function clearToolActivity() {
    var list = document.querySelector('#tool-activity .tool-activity-list');
    if (list) {
      // Stop every live card timer before clearing the list so no interval
      // outlives its card (issue #1070).
      var cards = list.querySelectorAll('[data-tool-key]');
      for (var i = 0; i < cards.length; i++) {
        stopToolCardTimer(cards[i].getAttribute('data-tool-key'));
      }
      list.innerHTML = '';
    }
    S.toolArgs = {};
    S.toolNames = {};
  }
  S.clearToolActivity = clearToolActivity;

  function resetActivityTracking() {
    stopAllToolCardTimers();
    S.toolCardTimers = {};
    S.toolCardElapsed = {};
    S.toolArgs = {};
    S.toolNames = {};
  }
  S.resetActivityTracking = resetActivityTracking;

  function injectToolCardSlot(sessionId, packet, toolCallKey) {
    // Store args for later use in renderToolCard (tool_result doesn't carry args)
    if (packet.args) {
      S.toolArgs[toolCallKey] = packet.args;
    }
    var list = document.querySelector('#tool-activity .tool-activity-list');
    if (!list) return;

    var toolName = packet.tool || packet.name || 'tool';
    S.toolNames[toolCallKey] = toolName;

    // Idempotent: skip if already exists (e.g. SSE reconnect replay of a
    // tool_call whose card survived the reconnect)
    if (list.querySelector('[data-tool-key="' + toolCallKey + '"]')) return;

    // Max 6 entries — FIFO eviction
    var existingWrappers = list.querySelectorAll('.tool-entry-wrapper');
    while (existingWrappers.length >= 6) {
      var firstKey = existingWrappers[0].getAttribute('data-tool-key');
      if (firstKey) {
        pruneToolCardState(firstKey);
      }
      existingWrappers[0].remove();
      existingWrappers = list.querySelectorAll('.tool-entry-wrapper');
    }

    // Create <details> element — the tool entry itself, no extra layers
    var details = document.createElement('details');
    details.className = 'tool-entry-wrapper';
    details.id = toolCallKey;
    details.setAttribute('data-tool-key', toolCallKey);
    details.innerHTML = '<summary class="tool-entry tool-running">' +
      '<span class="tool-icon">\uD83D\uDD27</span>' +
      '<span class="tool-name">' + S.escapeHtml(toolName) + '</span>' +
      '<span class="tool-status-label">running...</span>' +
      '<span class="tool-elapsed" data-tool-elapsed="' + toolCallKey + '"></span>' +
      '<span class="tool-chevron">\u25B8</span>' +
      '</summary>';

    list.appendChild(details);

    // Start live elapsed timer
    var startMs = Date.now();
    S.toolCardElapsed[toolCallKey] = { startMs: startMs, finalMs: null };
    startToolCardTimer(toolCallKey);
  }
  S.injectToolCardSlot = injectToolCardSlot;

  function findToolCardSlot(toolCallKey) {
    return document.querySelector('#tool-activity [data-tool-key="' + toolCallKey + '"]');
  }
  S.findToolCardSlot = findToolCardSlot;

  // Replay-stable identity for a tool_call packet: (turn, tool, args). The
  // server replays the retention window on reconnect, so the SAME logical tool
  // call must map to the SAME tool card key across replays (issue #1070).
  function toolIdentityForPacket(packet) {
    var args = packet.args || packet.Args || '';
    var argsStr = (typeof args === 'string') ? args : JSON.stringify(args);
    return (packet.turn || 0) + ':' + (packet.tool || packet.name || 'tool') + ':' + argsStr;
  }
  S.toolIdentityForPacket = toolIdentityForPacket;

  function renderToolCard(sessionId, type, packet, toolCallKey) {
    if (!toolCallKey) return;

    // Stop live timer and record final elapsed
    stopToolCardTimer(toolCallKey);
    var finalElapsed = '';
    if (S.toolCardElapsed[toolCallKey] && S.toolCardElapsed[toolCallKey].startMs) {
      // Keep the originally recorded final elapsed when a replayed tool_result
      // (reconnect replay) updates an already-done card, instead of inflating
      // it with wall-clock time spent disconnected (issue #1070).
      var elapsedMs = S.toolCardElapsed[toolCallKey].finalMs ||
        (Date.now() - S.toolCardElapsed[toolCallKey].startMs);
      S.toolCardElapsed[toolCallKey].finalMs = elapsedMs;
      finalElapsed = S.formatTimer(elapsedMs);
    }

    // Detect tool error from output
    var output = packet.output || '';
    var isError = typeof output === 'string' && output.indexOf('Tool error:') === 0;

    // Get saved args
    var argsStr = S.toolArgs[toolCallKey] || packet.args || packet.Args || '';
    var argsObj = null;
    if (typeof argsStr === 'string' && argsStr) {
      try { argsObj = JSON.parse(argsStr); } catch(e) {}
    } else if (typeof argsStr === 'object' && argsStr) {
      argsObj = argsStr;
    }

    // Find the details element in sidebar
    var details = document.querySelector('#tool-activity details[data-tool-key="' + toolCallKey + '"]');
    if (!details) return;

    // Update summary to done/error state
    var summary = details.querySelector('.tool-entry');
    if (summary) {
      summary.className = 'tool-entry ' + (isError ? 'tool-error' : 'tool-done');
      var icon = summary.querySelector('.tool-icon');
      if (icon) icon.textContent = isError ? '\u274C' : '\u2705';
      var label = summary.querySelector('.tool-status-label');
      if (label) label.textContent = isError ? 'error' : 'done';
      var elapsedSpan = summary.querySelector('.tool-elapsed');
      if (elapsedSpan && finalElapsed) elapsedSpan.textContent = '\u2191 ' + finalElapsed;
    }

    // Build output content — command line from args + result output
    var outputContent = '';
    if (argsObj && argsObj.command) {
      outputContent += '<div class="tool-command"><span class="tool-prompt">$</span> <span class="tool-command-text">' + S.escapeHtml(argsObj.command) + '</span></div>';
    }
    if (output) {
      outputContent += '<pre class="tool-result"><code>' + S.escapeHtml(output) + '</code></pre>';
    }
    // Only add content once (idempotent)
    if (outputContent && !details.querySelector('.tool-result')) {
      details.insertAdjacentHTML('beforeend', outputContent);
    }
  }
  S.renderToolCard = renderToolCard;

  function renderComponent(sessionId, packet, toolCallKey) {
    // The SSE 'component' event nests name/data inside packet.data:
    //   {"type":"component","data":{"name":"MermaidDiagram","data":{...}}}
    var nested = packet.data || {};
    const compName = nested.name || '';
    const compData = nested.data || {};
    if (!compName) {
      console.warn('[eitri] renderComponent: no compName, packet.data=', JSON.stringify(packet.data));
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
  S.renderComponent = renderComponent;
})();
