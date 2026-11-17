package main

import (
	"path/filepath"
	"testing"
)

func TestSecurityPolicy_ValidatePath(t *testing.T) {
	workDir := t.TempDir()
	policy, err := NewSecurityPolicy(workDir, nil)
	if err != nil {
		t.Fatalf("NewSecurityPolicy failed: %v", err)
	}

	valid := []struct {
		input string
		want  string
	}{
		{input: ".", want: workDir},
		{input: "file.txt", want: filepath.Join(workDir, "file.txt")},
		{input: "sub/dir/file.txt", want: filepath.Join(workDir, "sub", "dir", "file.txt")},
		{input: filepath.Join(workDir, "abs.txt"), want: filepath.Join(workDir, "abs.txt")},
	}
	for _, tt := range valid {
		got, err := policy.ValidatePath(tt.input)
		if err != nil {
			t.Errorf("ValidatePath(%q) failed: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ValidatePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}

	invalid := []string{
		"../outside.txt",
		"sub/../../outside.txt",
		"/etc/passwd",
		workDir + "2/sibling.txt",
	}
	for _, p := range invalid {
		if got, err := policy.ValidatePath(p); err == nil {
			t.Errorf("ValidatePath(%q) succeeded (%q), want error", p, got)
		}
	}
}

func TestSecurityPolicy_ValidatePath_RootWorkingDir(t *testing.T) {
	policy, err := NewSecurityPolicy("/", nil)
	if err != nil {
		t.Fatalf("NewSecurityPolicy(/) failed: %v", err)
	}

	for _, p := range []string{"/", "etc/passwd", "/etc/passwd"} {
		if _, err := policy.ValidatePath(p); err != nil {
			t.Errorf("ValidatePath(%q) under root workingDir failed: %v", p, err)
		}
	}
}

func TestSecurityPolicy_ValidateBashCommand(t *testing.T) {
	policy, err := NewSecurityPolicy(t.TempDir(), []string{
		`^ls\b`,
		`^pwd$`,
		`^git\s`,
		`^cat\b`,
	})
	if err != nil {
		t.Fatalf("NewSecurityPolicy failed: %v", err)
	}

	allowed := []string{
		"ls",
		"ls -la",
		"  ls -la  ",
		"pwd",
		"git status",
	}
	for _, cmd := range allowed {
		if err := policy.ValidateBashCommand(cmd); err != nil {
			t.Errorf("ValidateBashCommand(%q) failed: %v", cmd, err)
		}
	}

	rejected := []string{
		"",
		"   ",
		"rm -rf /",
		"ls; id",
		"ls | cat",
		"ls & id",
		"ls && id",
		"ls `id`",
		"ls $(id)",
		"ls $HOME",
		"ls (id)",
		"ls )",
		"ls > out.txt",
		"cat < in.txt",
		"ls\nid",
		"ls\n",
		"ls\rid",
		"ls\r",
	}
	for _, cmd := range rejected {
		if err := policy.ValidateBashCommand(cmd); err == nil {
			t.Errorf("ValidateBashCommand(%q) succeeded, want rejection", cmd)
		}
	}
}
