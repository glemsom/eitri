// eitri-stream — browser island orchestrator for the SSE stream lifecycle.
// Owns the /api/sessions/{id}/stream EventSource, packet dispatch, run status
// and dead-air timer. Companion modules (eitri-stream-common/toolcards/
// announcer/tokens/confirmation/scroll/render) share the window.eitriStream
// runtime and are loaded before this file.
(function () {
  'use strict';

  var S = window.eitriStream;

  function updateRunStatus(status, detail, state) {
    const statusText = document.querySelector('.stream-status-text');
    if (statusText) {
      statusText.textContent = S.statusLabel(status);
      // Set CSS class for visibility/color (issue #451)
      statusText.className = 'stream-status-text ' + status;
    }

    // Set glow status on the streaming message avatar (in the chat area)
    const avatarContainer = document.querySelector('.streaming-message .message-avatar-container');
    if (avatarContainer) {
      avatarContainer.setAttribute('data-stream-status', status);
    }

    // Toggle typing dots visibility (issue #450)
    const typingDots = document.querySelector('.typing-dots');
    if (typingDots) {
      if (status === S.STATES.CONNECTING || status === S.STATES.TOOL_RUNNING) {
        typingDots.hidden = false;
      } else {
        typingDots.hidden = true;
      }
    }
  }

  function ensureChatChrome() {
    const statusText = document.querySelector('.stream-status-text');
    if (!statusText) return;
    if (!statusText.textContent || !statusText.textContent.trim()) {
      updateRunStatus(S.STATES.IDLE, S.defaultStatusDetail(S.STATES.IDLE), null);
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
      if (!state.firstEventSeen && state.status === S.STATES.CONNECTING) {
        updateRunStatus(S.STATES.CONNECTING, 'Working — waiting for first response or tool activity.', state);
      }
    }, S.NO_DEAD_AIR_MS);
  }

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

  function connectStream(sessionId) {
    disconnectStream(sessionId);
    S.stopAllToolCardTimers();
    S.resetActivityTracking();
    S.clearThinkingPanel();
    S.clearToolActivity();

    const state = S.createStreamState();
    state.status = S.STATES.CONNECTING;
    S.streams.set(sessionId, { eventSource: null, state });
    updateRunStatus(S.STATES.CONNECTING, S.defaultStatusDetail(S.STATES.CONNECTING, state), state);
    armDeadAirTimer(state);

    const es = new EventSource('/api/sessions/' + sessionId + '/stream');

    es.onopen = function () {
      if (state.awaitingResume) {
        updateRunStatus(S.STATES.RECONNECTING, 'Reconnected. Waiting for stream to resume.', state);
        return;
      }
      updateRunStatus(S.STATES.CONNECTING, S.defaultStatusDetail(S.STATES.CONNECTING, state), state);
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
      if (state.status === S.STATES.DONE || state.status === S.STATES.ERROR || state.status === S.STATES.IDLE || state.status === S.STATES.RENDERING) {
        es.close();
        return;
      }
      clearDeadAirTimer(state);
      // Do NOT clear tool activity, thinking content, or elapsed tracking here:
      // a transient EventSource error (network blip, proxy timeout) must not
      // destroy in-flight tool UI. The server replays the retention window on
      // reconnect and tool cards/elapsed timers resume where they left off
      // (issue #1070).
      state.awaitingResume = state.firstEventSeen;
      state.status = S.STATES.RECONNECTING;
      updateRunStatus(S.STATES.RECONNECTING, S.defaultStatusDetail(S.STATES.RECONNECTING, state), state);
    };

    const entry = S.streams.get(sessionId);
    if (entry) entry.eventSource = es;
    else S.streams.set(sessionId, { eventSource: es, state });
  }

  function disconnectStream(sessionId) {
    // If a run ends (done/error/closed/cancel) while a confirmation modal is
    // open, the modal's full-screen overlay (z-index:1000) would otherwise stay
    // and block ALL clicks — including the header, making the whole UI
    // unresponsive. Close it on any stream teardown. Idempotent.
    S.closeConfirmationModal();
    const entry = S.streams.get(sessionId);
    if (!entry) return;
    clearDeadAirTimer(entry.state);
    S.clearStreamTimer(entry.state);
    S.clearStreamAnnounceTimer(entry.state);
    S.stopAllToolCardTimers();
    if (entry.eventSource) {
      entry.eventSource.close();
    }
    S.streams.delete(sessionId);
  }

  function disconnectAll() {
    for (const [id] of S.streams) {
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
        state.status = S.STATES.CONNECTING;
        updateRunStatus(S.STATES.CONNECTING, S.defaultStatusDetail(S.STATES.CONNECTING, state), state);
        armDeadAirTimer(state);
        break;

      case 'thinking_delta':
        markStreamResumed(state);
        state.status = S.STATES.STREAMING;
        // Ensure streaming bubble exists so the avatar glow shows in the chat area
        S.showStreamingBubble();
        updateRunStatus(S.STATES.STREAMING, S.defaultStatusDetail(S.STATES.STREAMING, state), state);
        S.appendThinkingDelta(packet.content);
        break;

      case 'token':
        // Replayed tokens belong to turns already committed and rendered as
        // final bubbles by the server on page load; appending them again would
        // duplicate the message after a session switch-back. The server marks
        // every history-replayed event with `replayed`; live events are not
        // marked and continue to stream normally.
        if (packet.replayed) break;
        markStreamResumed(state);
        state.status = S.STATES.STREAMING;
        S.showStreamingBubble();
        // Insert paragraph break between turns (after tool calls)
        if (state.needsSectionBreak) {
          packet.content = '\n\n' + packet.content;
          state.needsSectionBreak = false;
        }
        updateRunStatus(S.STATES.STREAMING, S.defaultStatusDetail(S.STATES.STREAMING, state), state);
        S.appendToken(state, packet.content);
        break;

      case 'tool_call':
        markStreamResumed(state);
        state.status = S.STATES.TOOL_RUNNING;
        updateRunStatus(S.STATES.TOOL_RUNNING, 'Running tool: ' + (packet.tool || 'unknown tool'), state);

        // Tool card keys are derived from the packet's replay-stable identity
        // (turn + tool + args) instead of Date.now: when the server replays the
        // retention window after a reconnect, a replayed tool_call resolves to
        // the SAME card that survived the reconnect instead of creating a
        // duplicate (issue #1070).
        var identity = S.toolIdentityForPacket(packet);
        if (!state.toolKeysByIdentity[identity]) {
          S.toolEntryCounter++;
          state.toolKeysByIdentity[identity] = sessionId + '-tool-' + Date.now() + '-' + S.toolEntryCounter;
        }
        var toolCallKey = state.toolKeysByIdentity[identity];
        state.lastToolCallKey = toolCallKey;

        // Skip tool card for render_quick_replies — the actual quick reply chips
        // appear inline on the next assistant message (via InlineQuickReplies).
        // Showing a tool card with "Rendered QuickReplies with options: …" is noise.
        if (packet.tool === 'render_quick_replies') {
          // Ensure streaming bubble exists for whatever follows
          S.showStreamingBubble();
          break;
        }

        // Inject running tool card into sidebar (issue #320)
        S.injectToolCardSlot(sessionId, packet, toolCallKey);
        break;

      case 'tool_result':
        markStreamResumed(state);
        state.status = S.STATES.STREAMING;
        updateRunStatus(S.STATES.STREAMING, 'Tool finished. Continuing response.', state);

        // Skip tool card render for render_quick_replies (see tool_call above)
        if (packet.tool === 'render_quick_replies') {
          break;
        }

        // Next text token from the LLM starts a new section
        state.needsSectionBreak = true;

        S.renderToolCard(sessionId, 'tool_result', packet, state.lastToolCallKey);
        break;

      case 'context_update':
        markStreamResumed(state);
        state.status = S.STATES.STREAMING;
        updateRunStatus(S.STATES.STREAMING, 'Processing context.', state);
        if (typeof window.dispatchContextUpdate === 'function') {
          window.dispatchContextUpdate(packet.data);
        }
        break;

      case 'skill_activated':
        markStreamResumed(state);
        state.status = S.STATES.STREAMING;
        updateRunStatus(S.STATES.STREAMING, 'Skill loaded: ' + (packet.tool || 'unknown'), state);

        // Fetch updated active skill chips from the server and swap them in
        S.fetchActiveSkillChips(sessionId);
        break;

      case 'component':
        // Components of committed messages are already server-rendered into
        // their bubbles on page load; a replayed component event would render
        // a duplicate; live components continue streaming normally.
        if (packet.replayed) break;
        markStreamResumed(state);
        S.renderComponent(sessionId, packet, state.lastToolCallKey);
        state.lastToolCallKey = '';
        break;

      case 'done':
        // A run finalizes exactly once: ignore duplicate/replayed 'done'
        // packets (SSE retention-window replay after a reconnect) once the run
        // is already finalizing or finalized — guard on run status (issue #1070).
        if (state.status === S.STATES.RENDERING || state.status === S.STATES.DONE) {
          break;
        }
        clearDeadAirTimer(state);
        state.status = S.STATES.RENDERING;
        updateRunStatus(S.STATES.RENDERING, S.defaultStatusDetail(S.STATES.RENDERING, state), state);
        // Flush any unannounced stream tail to the screen-reader live region
        // before the streaming bubble is replaced by the final render — the
        // last tokens must not go unannounced (issue #1071).
        S.flushStreamBuffer(state);
        S.flushStreamAnnounce(state);
        // Prevent reconnect cycle: set guard BEFORE finalizeMessage sends
        // the HTMX render POST. Otherwise htmx:beforeSwap (#streaming) →
        // disconnectAll → htmx:afterSwap → autoConnectOnPageLoad reconnects
        // to the SSE stream (still in the 5s retention window), replays
        // history including 'done', and renders a duplicate card.
        S.noActiveRunTimestamps[sessionId] = Date.now();
        S.showStreamingBubble();
        S.finalizeMessage(sessionId, packet.message_id, packet.usage, function () {
          state.status = S.STATES.DONE;
          updateRunStatus(S.STATES.DONE, S.defaultStatusDetail(S.STATES.DONE, state), state);
          disconnectStream(sessionId);
          reenableComposer();
        });
        break;

      case 'needs_confirmation':
        markStreamResumed(state);
        state.status = S.STATES.STREAMING;
        updateRunStatus(S.STATES.STREAMING, 'Awaiting user confirmation.', state);
        var path = packet.data && packet.data.path;
        var msg = packet.data && packet.data.message;
        if (!path) path = packet.content || '';
        if (!msg) msg = packet.content || '';
        S.showConfirmationModal(sessionId, path, msg);
        break;

      case 'error':
        if (typeof window.resetContextPanel === 'function') {
          window.resetContextPanel();
        }
        clearDeadAirTimer(state);
        state.status = S.STATES.ERROR;
        updateRunStatus(S.STATES.ERROR, packet.message || S.defaultStatusDetail(S.STATES.ERROR, state), state);
        // Announce whatever was received before the failure so a partially
        // streamed reply is not silently swallowed (issue #1071).
        S.flushStreamBuffer(state);
        S.flushStreamAnnounce(state);
        S.renderError(sessionId, packet.message);
        disconnectStream(sessionId);
        reenableComposer();
        break;

      case 'closed':
        if (typeof window.resetContextPanel === 'function') {
          window.resetContextPanel();
        }
        clearDeadAirTimer(state);
        updateRunStatus(S.STATES.IDLE, packet.message || 'Session closed.', state);
        disconnectStream(sessionId);
        break;

      case 'no_active_run':
        // No active run — go idle without retry
        clearDeadAirTimer(state);
        state.status = S.STATES.IDLE;
        updateRunStatus(S.STATES.IDLE, 'No active run.', state);
        // Record timestamp to prevent reconnect storms in autoConnectOnPageLoad
        S.noActiveRunTimestamps[sessionId] = Date.now();
        // Close the EventSource (no retry)
        if (S.streams.has(sessionId)) {
          var entry = S.streams.get(sessionId);
          if (entry && entry.eventSource) {
            entry.eventSource.close();
          }
          S.streams.delete(sessionId);
        }
        break;
    }
  }

  // Auto-connect stream for current session on page load
  function autoConnectOnPageLoad() {
    var sessionId = S.getSessionIdFromUrl();
    if (!sessionId || S.streams.has(sessionId)) return;
    // Guard against reconnect storms: skip if we got 'no_active_run' within 10s.
    var lastNoActive = S.noActiveRunTimestamps[sessionId];
    if (lastNoActive && Date.now() - lastNoActive < 10000) return;
    connectStream(sessionId);
  }

  document.addEventListener('eitri:connectRunStream', function (e) {
    const sessionId = S.extractSessionId(e.detail, e.target);
    if (!sessionId) return;
    // Clear any persisted context data for this session when a new run starts
    try {
      sessionStorage.removeItem('eitri-context-' + sessionId);
    } catch (e) {
      // ignore
    }
    connectStream(sessionId);
  });

  document.addEventListener('htmx:beforeSwap', function (evt) {
    const targetId = evt.detail && evt.detail.target && evt.detail.target.id;
    if (targetId === 'app' || targetId === 'chat-view' || targetId === 'streaming') {
      disconnectAll();
    }
  });

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () {
      ensureChatChrome();
      S.initCodeBlockButtons();
      S.initScrollToBottomButton();
    });
  } else {
    ensureChatChrome();
    S.initCodeBlockButtons();
    S.initScrollToBottomButton();
  }

  document.addEventListener('htmx:afterSwap', function () {
    ensureChatChrome();
    S.initCodeBlockButtons();
    S.reinitScrollObserver();
  });
  document.addEventListener('htmx:afterSettle', S.initCodeBlockButtons);

  // Add auto-connect to existing page load handlers (preserving existing init order)
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', autoConnectOnPageLoad);
  } else {
    autoConnectOnPageLoad();
  }
  document.addEventListener('htmx:afterSwap', function () {
    autoConnectOnPageLoad();
  });
})();
