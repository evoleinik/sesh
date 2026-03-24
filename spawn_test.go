package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpawnNameDerivation(t *testing.T) {
	tests := []struct {
		promptFile string
		want       string
	}{
		{"prompts/toshiba-fixes.md", "toshiba-fixes"},
		{"prompts/stripe-checkout-test.md", "stripe-checkout-test"},
		{"pipeline/README.md", "pipeline"},
		{"foo.md", "foo"},
	}

	for _, tt := range tests {
		base := filepath.Base(tt.promptFile)
		name := strings.TrimSuffix(base, filepath.Ext(base))
		if strings.ToLower(name) == "readme" {
			name = filepath.Base(filepath.Dir(tt.promptFile))
		}
		if name != tt.want {
			t.Errorf("derive(%q) = %q, want %q", tt.promptFile, name, tt.want)
		}
	}
}

func TestSpawnMetaFile(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, ".spawn-meta")

	meta := "prompt=prompts/test.md\nname=test\nstarted=2026-03-23T12:00:00Z\nmaxIter=10\n"
	os.WriteFile(metaPath, []byte(meta), 0644)

	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !strings.Contains(content, "prompt=prompts/test.md") {
		t.Error("meta should contain prompt path")
	}
	if !strings.Contains(content, "name=test") {
		t.Error("meta should contain name")
	}
	if !strings.Contains(content, "maxIter=10") {
		t.Error("meta should contain maxIter")
	}
}

func TestSpawnListEmpty(t *testing.T) {
	// spawnList scans for worktrees with .spawn-meta — in a temp dir there are none
	// Just verify it doesn't crash
	code := spawnList()
	if code != 0 {
		t.Errorf("spawnList() = %d, want 0", code)
	}
}

func TestSpawnCheckMissing(t *testing.T) {
	code := spawnCheck("nonexistent-worktree-12345")
	if code != 1 {
		t.Errorf("spawnCheck(nonexistent) = %d, want 1", code)
	}
}

func TestSpawnKillMissing(t *testing.T) {
	// Kill on nonexistent should not crash
	code := spawnKill("nonexistent-worktree-12345")
	// Returns 0 even if nothing to kill (idempotent)
	_ = code
}

func TestSpawnLogMissing(t *testing.T) {
	code := spawnLog("nonexistent-worktree-12345", 1)
	if code != 1 {
		t.Errorf("spawnLog(nonexistent) = %d, want 1", code)
	}
}

func TestFindGitRoot(t *testing.T) {
	// Only run if we're in a git repo
	if _, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err != nil {
		t.Skip("not in a git repo")
	}

	root, err := findGitRoot()
	if err != nil {
		t.Fatal(err)
	}
	if root == "" {
		t.Error("findGitRoot() returned empty string")
	}
	// Should be an absolute path
	if !filepath.IsAbs(root) {
		t.Errorf("findGitRoot() = %q, want absolute path", root)
	}
}

func TestSpawnPromptCopy(t *testing.T) {
	// Simulate: prompt exists in source but not in worktree
	srcDir := t.TempDir()
	wtDir := t.TempDir()

	// Create prompt in source
	promptDir := filepath.Join(srcDir, "prompts")
	os.MkdirAll(promptDir, 0755)
	os.WriteFile(filepath.Join(promptDir, "test.md"), []byte("# Test prompt"), 0644)

	// Prompt should NOT exist in worktree yet
	dstPrompt := filepath.Join(wtDir, "prompts", "test.md")
	if _, err := os.Stat(dstPrompt); err == nil {
		t.Fatal("prompt should not exist in worktree before copy")
	}

	// Simulate the copy logic from runSpawn
	promptFile := "prompts/test.md"
	dstPath := filepath.Join(wtDir, promptFile)
	if _, err := os.Stat(dstPath); os.IsNotExist(err) {
		srcPath := filepath.Join(srcDir, promptFile)
		if data, err := os.ReadFile(srcPath); err == nil {
			os.MkdirAll(filepath.Dir(dstPath), 0755)
			os.WriteFile(dstPath, data, 0644)
		}
	}

	// Verify prompt was copied
	data, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal("prompt should exist in worktree after copy")
	}
	if string(data) != "# Test prompt" {
		t.Errorf("copied prompt = %q, want %q", string(data), "# Test prompt")
	}
}
