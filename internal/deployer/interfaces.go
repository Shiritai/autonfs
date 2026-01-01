package deployer

// SSHClient abstracts the remote connection operations
type SSHClient interface {
	Connect() error
	Close() error
	RunTerminal(cmd string) error
	RunCommand(cmd string) (string, error)
	Scp(localPath, remotePath string) error
	// Helper to get underlying client info if needed, or abstract copy/exec methods
	// Ideally we abstract file transfer too but RunTerminal handles most.
	// For file transfer we used `scpToRemote` which uses `exec.Command("scp")`.
	// We should probably abstract File Upload too.
}

// FileUploader abstracts file transfer operations
type FileUploader interface {
	Upload(localPath, remotePath string) error
	WriteRemoteFile(content []byte, remotePath string) error
}

// RemoteExecutor combines both
type RemoteExecutor interface {
	SSHClient
	FileUploader
}
