package builder

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// BuildForArch Cross-compilation
// remoteArch: Target architecture (e.g. x86_64, aarch64)
// source: Source directory (e.g. "./cmd/autonfs")
// output: Output file path
func BuildForArch(remoteArch, source, output string) error {
	// 1. Map uname -m to Go's GOARCH
	goArch := remoteArch
	switch remoteArch {
	case "x86_64":
		goArch = "amd64"
	case "aarch64":
		goArch = "arm64"
	case "armv7l":
		goArch = "arm"
	}

	fmt.Printf("Compiling Target: GOOS=linux GOARCH=%s Src=%s -> %s\n", goArch, source, output)

	cmd := exec.Command("go", "build", "-o", output, source)
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goArch, "CGO_ENABLED=0") // Pure Go!
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// LocalBinaryPath returns the path of the current executable, used for self-replication (if architecture matches)
func LocalBinaryPath() (string, error) {
	return os.Executable()
}

// IsSameArch checks if the remote architecture matches the local one
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
