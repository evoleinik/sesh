package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestBuildBinary(t *testing.T) {
	// Just verify it builds
	cmd := exec.Command("go", "build", "-o", "/dev/null", ".")
	cmd.Dir = "."
	if err := cmd.Run(); err != nil {
		t.Fatalf("build failed: %v", err)
	}
}

func TestSubcommandDispatch(t *testing.T) {
	// Build binary for testing
	binPath := t.TempDir() + "/sesh"
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	tests := []struct {
		args     []string
		wantExit int
	}{
		{[]string{}, 1},                                    // no subcommand
		{[]string{"unknown-cmd"}, 1},                       // unknown
		{[]string{"--help"}, 0},                            // help
		{[]string{"--version"}, 0},                         // version
		{[]string{"digest"}, 1},                            // digest without args
		{[]string{"doctor"}, 0},                            // doctor (no events is ok)
	}

	for _, tt := range tests {
		t.Run(joinArgs(tt.args), func(t *testing.T) {
			cmd := exec.Command(binPath, tt.args...)
			cmd.Env = os.Environ()
			err := cmd.Run()

			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					t.Fatalf("unexpected error: %v", err)
				}
			}

			if exitCode != tt.wantExit {
				t.Errorf("args=%v exit=%d, want %d", tt.args, exitCode, tt.wantExit)
			}
		})
	}
}

func TestDigestFlags(t *testing.T) {
	binPath := t.TempDir() + "/sesh"
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Test digest with --json flag on sample data
	cmd = exec.Command(binPath, "digest", "--json", "testdata/sample.jsonl")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("digest --json: %v", err)
	}

	if len(out) == 0 {
		t.Error("expected non-empty JSON output")
	}
}

func joinArgs(args []string) string {
	if len(args) == 0 {
		return "(no args)"
	}
	s := ""
	for i, a := range args {
		if i > 0 {
			s += " "
		}
		s += a
	}
	return s
}
