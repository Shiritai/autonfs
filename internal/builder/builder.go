package builder

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// BuildForArch 交叉編譯
// remoteArch: 目標架構 (e.g. x86_64, aarch64)
// source: 原始碼目錄 (e.g. "./cmd/autonfs")
// output: 輸出檔案路徑
func BuildForArch(remoteArch, source, output string) error {
	// 1. 對應 uname -m 到 Go 的 GOARCH
	goArch := remoteArch
	switch remoteArch {
	case "x86_64":
		goArch = "amd64"
	case "aarch64":
		goArch = "arm64"
	case "armv7l":
		goArch = "arm"
	}

	fmt.Printf("正在編譯 Target: GOOS=linux GOARCH=%s Src=%s -> %s\n", goArch, source, output)
	
	cmd := exec.Command("go", "build", "-o", output, source)
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goArch, "CGO_ENABLED=0") // Pure Go!
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// LocalBinaryPath 回傳當前執行檔的路徑，用於複製自身 (如果架構相同)
func LocalBinaryPath() (string, error) {
	return os.Executable()
}

// IsSameArch 檢查遠端架構是否與本機相同
func IsSameArch(remoteArch string) bool {
	localArch := runtime.GOARCH
	convertedRemote := remoteArch
	switch remoteArch {
	case "x86_64":
		convertedRemote = "amd64"
	case "aarch64":
		convertedRemote = "arm64"
	case "armv7l":
		convertedRemote = "arm"
	}
	return localArch == convertedRemote
}
