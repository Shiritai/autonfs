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
		t.Errorf("ExpandPath 錯誤: got %s, want %s", result, expected)
	}
}

// 註：完整的 SSH Config Parser 測試需要 Mock 檔案系統，
// 為了 Phase 1 簡潔，我們先手動驗證，後續引入 testify 庫再做完整 Mock。