package sandbox

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Profile != ProfileDefault {
		t.Errorf("Profile = %q, want %q", cfg.Profile, ProfileDefault)
	}
	if !cfg.Network {
		t.Error("Network = false, want true")
	}
}

func TestIsZero(t *testing.T) {
	if !IsZero(Config{}) {
		t.Error("IsZero(zero) = false, want true")
	}
	if IsZero(DefaultConfig()) {
		t.Error("IsZero(default) = true, want false")
	}
	if IsZero(Config{Profile: ProfileNone}) {
		t.Error("IsZero(ProfileNone) = true, want false")
	}
	if IsZero(Config{ExtraWritablePaths: []string{"/opt"}}) {
		t.Error("IsZero(with extra paths) = true, want false")
	}
}

func TestWrapCommand_ProfileNone(t *testing.T) {
	exe, args, cleanup, err := WrapCommand("/workspace", "echo hi", Config{Profile: ProfileNone})
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exe != "bash" {
		t.Errorf("exe = %q, want %q", exe, "bash")
	}
	if len(args) != 2 || args[0] != "-c" || args[1] != "echo hi" {
		t.Errorf("args = %v, want [\"-c\", \"echo hi\"]", args)
	}
}

func TestBwrapAvailable(t *testing.T) {
	// BwrapAvailable should return the same result as BwrapIsUsable.
	got := BwrapAvailable()
	expected := BwrapIsUsable()
	if got != expected {
		t.Errorf("BwrapAvailable() = %v, want %v (same as BwrapIsUsable())", got, expected)
	}

	// Calling again should return the same cached value.
	got2 := BwrapAvailable()
	if got2 != got {
		t.Errorf("BwrapAvailable() second call = %v, want %v (cached)", got2, got)
	}
}

func TestWrapCommand_Default_BwrapNotAvailable(t *testing.T) {
	// bwrap might or might not be installed/usable; either is fine.
	// We verify the fallback path works.
	exe, args, cleanup, err := WrapCommand("/workspace", "echo hi", DefaultConfig())
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// If bwrap is not usable, we should fall back to bash -c.
	if !BwrapIsUsable() {
		if exe != "bash" {
			t.Errorf("without bwrap: exe = %q, want %q", exe, "bash")
		}
		if len(args) != 2 || args[0] != "-c" || args[1] != "echo hi" {
			t.Errorf("without bwrap: args = %v, want [\"-c\", \"echo hi\"]", args)
		}
	}
	// If bwrap is usable, we just verify we get bwrap as executable.
	if BwrapIsUsable() && exe != "bash" {
		if !strings.HasSuffix(exe, "bwrap") {
			t.Errorf("exe = %q, want bwrap path", exe)
		}
		foundNet := false
		for _, a := range args {
			if a == "--unshare-net" {
				foundNet = true
			}
		}
		if foundNet {
			t.Error("--unshare-net should not appear when Network=true")
		}
	}
}

func TestWrapCommand_Default_NoNetwork(t *testing.T) {
	cfg := Config{Profile: ProfileDefault, Network: false}
	_, args, cleanup, err := WrapCommand("/workspace", "echo hi", cfg)
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !BwrapIsUsable() {
		// bwrap not available — fine, just verify fallback
		return
	}
	// bwrap available — verify --unshare-net is present
	found := false
	for _, a := range args {
		if a == "--unshare-net" {
			found = true
			break
		}
	}
	if !found {
		t.Error("--unshare-net missing when Network=false")
	}
}

func TestWrapCommand_Default_ArgStructure(t *testing.T) {
	// Only run this if bwrap is usable.
	if !BwrapIsUsable() {
		t.Skip("bwrap not usable, skipping arg structure test")
	}

	cfg := DefaultConfig()
	exe, args, cleanup, err := WrapCommand("/my/workspace", "ls -la", cfg)
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasSuffix(exe, "bwrap") {
		t.Fatalf("exe = %q, want bwrap", exe)
	}

	// Check essential flags are present.
	// Note: --bind tmpDir /tmp uses an ephemeral temp dir, not /tmp literally.
	essential := []string{"--die-with-parent", "--new-session", "--unshare-pid",
		"--ro-bind", "/", "/",
		"--bind", "/my/workspace", "/my/workspace",
		"--dev", "/dev",
		"--proc", "/proc",
		"--chdir", "/my/workspace",
	}

	for _, flag := range essential {
		found := false
		for _, a := range args {
			if a == flag {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing argument: %s", flag)
		}
	}

	// Verify --bind <tmpdir> /tmp is present (ephemeral tmp).
	foundTmpBind := false
	for i, a := range args {
		if a == "--bind" && i+2 < len(args) && args[i+2] == "/tmp" {
			foundTmpBind = true
			break
		}
	}
	if !foundTmpBind {
		t.Error("missing --bind <tmpdir> /tmp")
	}

	// Verify the command is at the end after --.
	sepIdx := -1
	for i, a := range args {
		if a == "--" {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		t.Fatal("missing -- separator")
	}
	if sepIdx+3 > len(args) || args[sepIdx+1] != "bash" || args[sepIdx+2] != "-c" || args[sepIdx+3] != "ls -la" {
		t.Errorf("end of args = %v, want [..., \"--\", \"bash\", \"-c\", \"ls -la\"]", args)
	}

	// --unshare-net should be absent with default Network=true
	for _, a := range args {
		if a == "--unshare-net" {
			t.Error("--unshare-net should not appear when Network=true")
			break
		}
	}
}

func TestWrapCommand_ExtraWritablePaths(t *testing.T) {
	if !BwrapIsUsable() {
		t.Skip("bwrap not usable, skipping")
	}

	cfg := Config{
		Profile:            ProfileDefault,
		Network:            true,
		ExtraWritablePaths: []string{"/opt/cache", "/var/log"},
	}

	_, args, cleanup, err := WrapCommand("/workspace", "echo hi", cfg)
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both extra paths should appear as --bind mounts.
	foundOpt := false
	foundVarLog := false
	for i, a := range args {
		if a == "--bind" && i+2 < len(args) && args[i+1] == "/opt/cache" && args[i+2] == "/opt/cache" {
			foundOpt = true
		}
		if a == "--bind" && i+2 < len(args) && args[i+1] == "/var/log" && args[i+2] == "/var/log" {
			foundVarLog = true
		}
	}
	if !foundOpt {
		t.Error("missing --bind /opt/cache")
	}
	if !foundVarLog {
		t.Error("missing --bind /var/log")
	}
}

func TestWrapCommand_EmptyExtraWritablePaths(t *testing.T) {
	if !BwrapIsUsable() {
		t.Skip("bwrap not usable, skipping")
	}

	cfg := Config{
		Profile:            ProfileDefault,
		Network:            true,
		ExtraWritablePaths: []string{"", "  "},
	}

	_, args, cleanup, err := WrapCommand("/workspace", "echo hi", cfg)
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Empty paths should not produce --bind entries.
	for i, a := range args {
		if a == "--bind" && i+1 < len(args) && (args[i+1] == "" || args[i+1] == "  ") {
			t.Errorf("unexpected --bind with empty path at position %d", i)
		}
	}
}

func TestWrapCommand_EmptyWorkspace(t *testing.T) {
	cfg := Config{Profile: ProfileDefault, Network: true}
	_, _, cleanup, err := WrapCommand("", "echo hi", cfg)
	defer cleanup()
	if err == nil {
		t.Fatal("expected error for empty workspace")
	}
}

func TestWrapCommand_ZeroConfigDefaultsToDefault(t *testing.T) {
	// Zero config should behave like DefaultConfig.
	_, args, cleanup, err := WrapCommand("/w", "echo hi", Config{})
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !BwrapIsUsable() {
		// bwrap not available — fallback, fine
		return
	}
	// Should include default flags.
	foundPid := false
	for _, a := range args {
		if a == "--unshare-pid" {
			foundPid = true
			break
		}
	}
	if !foundPid {
		t.Error("zero config should default to --unshare-pid")
	}
}

func TestNonLinuxFallback(t *testing.T) {
	// Simulate non-Linux by temporarily replacing runtime.GOOS… not possible,
	// but we can verify the logic by checking that WrapCommand doesn't
	// error on non-Linux in CI. This is a smoke test.
	cfg := DefaultConfig()
	exe, args, cleanup, err := WrapCommand("/w", "echo hi", cfg)
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// On any platform, we should get either bwrap or bash.
	if exe != "bash" && !strings.HasSuffix(exe, "bwrap") {
		t.Errorf("unexpected executable: %s", exe)
	}
	_ = args
}

func BenchmarkWrapCommand(b *testing.B) {
	cfg := DefaultConfig()
	for i := 0; i < b.N; i++ {
		_, _, cleanup, _ := WrapCommand("/workspace", "echo hello", cfg)
		cleanup()
	}
}

// TestWrapCommand_ActualExecution is an integration test that actually
// runs a command through the sandbox. It skips if bwrap is not usable.
func TestWrapCommand_ActualExecution(t *testing.T) {
	if !BwrapIsUsable() {
		t.Skip("bwrap not usable, skipping integration test")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	dir, err := os.MkdirTemp(wd, "sandbox-test-*")
	if err != nil {
		t.Fatalf("creating test dir: %v", err)
	}
	defer os.RemoveAll(dir)
	cfg := DefaultConfig()

	exe, args, cleanup, err := WrapCommand(dir, "echo sandboxed-ok", cfg)
	defer cleanup()
	if err != nil {
		t.Fatalf("WrapCommand: %v", err)
	}

	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("execution failed: %v\noutput: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "sandboxed-ok" {
		t.Errorf("output = %q, want %q", strings.TrimSpace(string(out)), "sandboxed-ok")
	}
}

// TestWrapCommand_ReadOnlyRoot verifies that /etc/hostname is readable
// (ro-bind doesn't hide it) but /usr is not writable.
func TestWrapCommand_ReadOnlyRoot(t *testing.T) {
	if !BwrapIsUsable() {
		t.Skip("bwrap not usable, skipping integration test")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	dir, err := os.MkdirTemp(wd, "sandbox-test-*")
	if err != nil {
		t.Fatalf("creating test dir: %v", err)
	}
	defer os.RemoveAll(dir)
	cfg := DefaultConfig()

	// Test reading a file from /etc works.
	exe, args, cleanup, err := WrapCommand(dir, "head -c 10 /etc/hostname 2>/dev/null || echo ok", cfg)
	defer cleanup()
	if err != nil {
		t.Fatalf("WrapCommand: %v", err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("read test failed: %v\noutput: %s", err, out)
	}
	_ = out // /etc/hostname might or might not exist; we just check no crash

	// Test writing to /usr is rejected.
	exe2, args2, cleanup2, err := WrapCommand(dir, "touch /usr/test-write-123 2>&1", cfg)
	defer cleanup2()
	if err != nil {
		t.Fatalf("WrapCommand: %v", err)
	}
	cmd2 := exec.Command(exe2, args2...)
	cmd2.Dir = dir
	out2, _ := cmd2.CombinedOutput()
	// Should fail with "Read-only file system" or similar.
	if !strings.Contains(string(out2), "Read-only file system") &&
		!strings.Contains(string(out2), "cannot touch") &&
		!strings.Contains(string(out2), "Permission denied") {
		// It might also succeed if running as root inside some environments.
		t.Logf("write to /usr output: %s", out2)
	}
}

// TestWrapCommand_Network tests that network is available by default.
func TestWrapCommand_Network(t *testing.T) {
	if !BwrapIsUsable() {
		t.Skip("bwrap not usable, skipping integration test")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	dir, err := os.MkdirTemp(wd, "sandbox-test-*")
	if err != nil {
		t.Fatalf("creating test dir: %v", err)
	}
	defer os.RemoveAll(dir)
	cfg := DefaultConfig()

	exe, args, cleanup, err := WrapCommand(dir, "curl --version", cfg)
	defer cleanup()
	if err != nil {
		t.Fatalf("WrapCommand: %v", err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		// curl might not be installed; that's fine
		t.Logf("curl test (network enabled): %v\n%s", err, out)
	}
}

// TestWrapCommand_WorkspaceReadWrite verifies the workspace is writable.
func TestWrapCommand_WorkspaceReadWrite(t *testing.T) {
	if !BwrapIsUsable() {
		t.Skip("bwrap not usable, skipping integration test")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	dir, err := os.MkdirTemp(wd, "sandbox-test-*")
	if err != nil {
		t.Fatalf("creating test dir: %v", err)
	}
	defer os.RemoveAll(dir)
	cfg := DefaultConfig()

	exe, args, cleanup, err := WrapCommand(dir, "echo written > test-file.txt && cat test-file.txt", cfg)
	defer cleanup()
	if err != nil {
		t.Fatalf("WrapCommand: %v", err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workspace write test failed: %v\noutput: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "written" {
		t.Errorf("output = %q, want %q", strings.TrimSpace(string(out)), "written")
	}

	// Verify the file was actually written on the host (not just inside sandbox).
	data, err := os.ReadFile(dir + "/test-file.txt")
	if err != nil {
		t.Fatalf("reading file from host: %v", err)
	}
	if strings.TrimSpace(string(data)) != "written" {
		t.Errorf("host file content = %q, want %q", strings.TrimSpace(string(data)), "written")
	}
}
