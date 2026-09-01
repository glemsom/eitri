# Session debug transcripts

Every Eitri run writes a GUID-named directory under `sessions/` in the data
directory (`~/.eitri` by default, override with `EITRI_DIR`). These transcripts
are the ground truth for analyzing Eitri's and the LLM's performance and
functionality. They are designed so an AI agent can navigate them without
loading whole sessions into its context window.

## Files in a session dir

| File | Content |
|---|---|
| `transcript.md` | Human-readable trail: prompts and final answers only (legacy, always written). |
| `messages.jsonl` | **Message-layer transcript**: one JSON line per provider request/response cycle — the exact messages sent to and received from the model. |
| `trace-request.http`, `trace-response.http` | Raw HTTP bodies; written only in debug mode (`-d`). |

## `messages.jsonl` schema

Each cycle appends two lines: one request record, one response record.

Request record:

```json
{"ts":"…","dir":"req","model":"<model>","messages":[<provider.Message wire shape>],"tools":["read_file", …]}
```

- `messages` is exactly what went over the wire this cycle: system prompt,
  user messages, assistant messages (including `reasoning_content` when the
  model produced reasoning), assistant `tool_calls`, and role `tool` results.
  If context overflow recovery ran, the next request shows the summarized or
  evicted history that was retried, so you can see what the model saw.
- `tools` is name-only to keep records compact; full schemas live in the
  engine's tool manifest.

Response record:

```json
{"ts":"…","dir":"resp","content":"…","reasoning_content":"…","tool_calls":[{"id":"…","type":"function","function":{"name":"…","arguments":"…"}}],"finish_reason":"stop|tool_calls|length|eof","usage":{"prompt_tokens":N,"completion_tokens":N,"prompt_cache_hit_tokens":N,"prompt_cache_miss_tokens":N},"error":"…"}
```

- `error` is present only when the turn failed (provider error or mid-stream failure).
- Cycles are implicitly numbered 1..N in file order ("turn N" below).

## Agent navigation workflow

Use the CLI instead of reading raw JSONL; it emits compact output designed for drill-down:

```
eitri session list                  # GUID, last activity, cycle count, model — newest first
eitri session show <guid>           # one compact line per cycle: model, tools, calls, tokens, errors, answer preview
eitri session show <guid> --turn N  # full pretty-printed JSON of that cycle's req+resp records only
# --no-reasoning (any 'show' invocation) strips chain-of-thought (reasoning_content) from output,
# so an agent can drill into a turn without pulling CoT text into its context
eitri session grep <pattern> [guid|all] [-full]
                                      # cycles whose content/reasoning/tool args match, with snippets;
                                      # -full prints the complete matching field text instead of a snippet
eitri session talk <guid>             # full conversation as plain text: [N] role: blocks, untruncated
#   --turn N|N-M    restrict to one turn or an inclusive range
#   --from N        start at turn N and run to the end
#   --role R        keep only user|assistant|tool|system messages (assistant responses included)
#   --reasoning     include chain-of-thought blocks (stripped by default)
#   --all           print every message of every request cycle; default dedupes the request
#                   history each cycle resends, so each user/tool message appears once
```

`talk` is the read-the-conversation command: it renders actual message bodies,
where `show` renders per-cycle metadata. Tool calls render as
`[N] assistant→tool name(args)` and tool results as `[N] tool(id):`. Use `talk`
to answer "what did the user say / what did the model reply", `grep -full` to
pull every message containing a string without truncation.

Recommended loop: `list` → pick a session → `show` to find the interesting
cycle (spike in tokens, an error, an odd tool call) → `--turn N` for full
detail → `grep` across all sessions to find similar behavior elsewhere. To
read the dialogue itself — user prompts and assistant replies in order — use
`talk`, optionally filtered with `--turn`/`--role`.

## Out of scope

The TUI does not render these transcripts; analysis is file- and CLI-only.
Old session dirs without `messages.jsonl` are simply skipped by `list`.
