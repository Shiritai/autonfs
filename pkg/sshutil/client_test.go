package sshutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home := os.Getenv("HOME")
	input := "~/test/file"
	expected := filepath.Join(home, "test/file")

	result := expandPath(input)
	if result != expected {
		t.Errorf("ExpandPath error: got %s, want %s", result, expected)
	}
}

// Note: A complete SSH Config Parser test requires a Mock filesystem.
// For Phase 1 simplicity, we verify manually. We will introduce testify library for full Mock later.
