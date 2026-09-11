package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/compress"
)

// bashBackend is the execution boundary behind the bash tool: either the bwrap
// sandbox or the unsandboxed direct runner selected by the --yolo-unsafe flag.
// Both honour the same environment contract (workspace cwd, session temp as
// TMPDIR) and return a bounded, ANSI-stripped, compressed output.
type bashBackend interface {
	Run(ctx context.Context, cmd string) (*Output, error)
	setTempHost(tempHost string)
}

// bashTool runs a shell command through the selected backend (bwrap sandbox by
// default; direct host execution in an unsandboxed --yolo-unsafe session), with
// host network, returning the combined stdout+stderr (token-efficient single stream).
type bashTool struct {
	backend     bashBackend
	unsandboxed bool
}

func (b *bashTool) Name() string {
	return "bash"
}

// BashTimeoutDefault is the time bound every bash call gets when the model
// requests none, and BashTimeoutMax is the ceiling a requested value is clamped
// to. They are the single source of the timeout policy the tool enforces; the
// UI reads them (via BashTimeout) so what it displays is what the run obeys.
const (
	BashTimeoutDefault = 120 * time.Second
	BashTimeoutMax     = 3600 * time.Second
)

// bashTimeoutContract is the model-facing statement of the timeout policy,
// derived from the bounds above so the description cannot drift from the code.
var bashTimeoutContract = fmt.Sprintf("Every call is time-bounded: the default limit is %d seconds, and a timed-out call may be retried with a larger `timeout` value (up to %d seconds).",
	int(BashTimeoutDefault.Seconds()), int(BashTimeoutMax.Seconds()))

// bashOutputContract is the shared, mode-independent description tail describing
// the bounded, ANSI-stripped, compressed output every bash run returns. It is
// identical across the sandboxed and unsandboxed tool definitions so the model
// sees the same recovery contract either way.
const bashOutputContract = "Returns the combined stream (stdout then stderr; ANSI escape sequences stripped, repeated consecutive lines collapsed). Output passes through a deterministic line compressor: heavy listings are truncated with an explicit \"+N more\" marker — never silent — so re-running the command is the recovery path if you need the tail. Same command yields the same compressed form."

func (b *bashTool) Description() string {
	if b.unsandboxed {
		return "Execute a shell command directly as your user on the host — no sandbox or cage is constructed, so the command runs with your full host permissions. " + bashTimeoutContract + " " + bashOutputContract
	}
	return "Execute a shell command in a sandbox. " + bashTimeoutContract + " " + bashOutputContract
}

func (b *bashTool) Schema() map[string]any {
	return strictSchema(map[string]any{
		"command": map[string]any{
			"type":        "string",
			"description": "The shell command to run, executed by /bin/bash -c.",
		},
		"timeout": map[string]any{
			"type":        "number",
			"description": fmt.Sprintf("Maximum seconds the command may run before it is stopped. Defaults to %d; may be raised up to %d.", int(BashTimeoutDefault.Seconds()), int(BashTimeoutMax.Seconds())),
		},
	}, []string{"command"})
}

func (b *bashTool) Run(ctx context.Context, args map[string]any) (ToolResult, error) {
	cmd, err := strArg(args, "command")
	if err != nil {
		return ToolResult{}, err
	}
	timeout, err := BashTimeout(args)
	if err != nil {
		return ToolResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	o, err := b.backend.Run(ctx, cmd)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return ToolResult{}, fmt.Errorf("timed out after %d seconds", int(timeout.Seconds()))
		}
		if o == nil {
			return ToolResult{}, err
		}
		return ToolResult{Text: o.Combined(), BytesDropped: o.Dropped}, err
	}
	text, compressed, dropped := compress.CompressResult(o.Combined())
	return ToolResult{Text: text, Compressed: compressed, Dropped: dropped, BytesDropped: o.Dropped}, nil
}

// BashTimeout resolves the effective time bound for one bash call from its
// arguments: the requested `timeout` seconds clamped to BashTimeoutMax, or
// BashTimeoutDefault when the argument is absent. It is exported so the UI can
// display the same bound the tool enforces instead of re-deriving the policy.
func BashTimeout(args map[string]any) (time.Duration, error) {
	v, ok := args["timeout"]
	if !ok {
		return BashTimeoutDefault, nil
	}
	var secs float64
	switch val := v.(type) {
	case float64:
		secs = val
	case int:
		secs = float64(val)
	case int64:
		secs = float64(val)
	default:
		return 0, fmt.Errorf("argument %q must be a number", "timeout")
	}
	if secs < 0 {
		return 0, fmt.Errorf("argument %q must be non-negative", "timeout")
	}
	d := time.Duration(secs * float64(time.Second))
	if d > BashTimeoutMax {
		d = BashTimeoutMax
	}
	return d, nil
}

// Combined returns stdout then stderr joined, prioritizing stdout for token efficiency while keeping stderr visible.
func (o *Output) Combined() string {
	switch {
	case o.Stdout != "" && o.Stderr != "":
		return strings.TrimSuffix(o.Stdout, "\n") + "\n" + o.Stderr
	case o.Stderr != "":
		return o.Stderr
	default:
		return o.Stdout
	}
}
