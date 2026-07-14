package executor

import "testing"

func TestTmuxSentinelParsingIgnoresTypedCommandLine(t *testing.T) {
	start := "EITRI_ST_abc"
	end := "EITRI_EN_abc"
	exit := "EITRI_EX_abc"

	paneBeforeExecution := "echo 'EITRI_ST_abc'; false; echo \"EITRI_EX_abc:$?\"; echo 'EITRI_EN_abc'\n" +
		" ╰❯ echo 'EITRI_ST_abc'; false; echo \"EITRI_EX_abc:$?\"; echo 'EITRI_EN_abc'\n"
	if containsMarkerLine(paneBeforeExecution, end) {
		t.Fatal("typed command line must not count as completed command output")
	}

	paneAfterExecution := paneBeforeExecution + start + "\n" + exit + ":1\n" + end + "\n"
	if !containsMarkerLine(paneAfterExecution, end) {
		t.Fatal("end marker output line should count as completed command output")
	}

	extracted := extractBetween(paneAfterExecution, start, end)
	if code := parseExitCode(extracted, exit); code != 1 {
		t.Fatalf("exit code = %d, want 1; extracted %q", code, extracted)
	}
}
