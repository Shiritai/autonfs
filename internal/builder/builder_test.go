package builder

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsSameArch(t *testing.T) {
	localArch := runtime.GOARCH
	
	// Test matching cases
	var matchingRemote string
	switch localArch {
	case "amd64":
		matchingRemote = "x86_64"
	case "arm64":
		matchingRemote = "aarch64"
	default:
		matchingRemote = localArch
	}

	if !IsSameArch(matchingRemote) {
		t.Errorf("IsSameArch(%q) should be true for local arch %q", matchingRemote, localArch)
	}

	// Test mismatch logic (assuming we are not running on riscv64)
	if IsSameArch("riscv64") {
		t.Errorf("IsSameArch(riscv64) should be false")
	}
}

func TestBuildForArch(t *testing.T) {
	// 1. Create a dummy source file
	tmpDir, err := os.MkdirTemp("", "autonfs_build_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srcFile := filepath.Join(tmpDir, "main.go")
	content := []byte("package main\nfunc main() { println(\"hello\") }")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Prepare output path
	outputPath := filepath.Join(tmpDir, "hello_bin")

	// 3. Build (Target x86_64/amd64 for generic check)
	// Note: We use the directory of srcFile as the package to build
	err = BuildForArch("x86_64", srcFile, outputPath)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// 4. Verify output exists
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Errorf("Output binary was not created at %s", outputPath)
	}
}
