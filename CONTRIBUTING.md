# Contributing to AutoNFS

Thank you for your interest in contributing to AutoNFS! We welcome contributions from the community to help make this project better.

## How to Contribute

### Reporting Bugs
If you find a bug, please open an issue on GitHub. Include as much detail as possible:
*   Steps to reproduce the issue.
*   Expected behavior vs. actual behavior.
*   Logs (use `journalctl -u autonfs-watcher` on the server).
*   Your OS and AutoNFS version.

### Suggesting Enhancements
We love new ideas! Please open an issue to discuss your feature request before implementing it. This ensures that your work aligns with the project's goals and avoids duplicate effort.

### Pull Requests
1.  **Fork the repository** and create your branch from `main`.
2.  **Make your changes**. Ensure your code follows the existing style and conventions.
3.  **Run tests**: `go test ./...`
4.  **Submit a Pull Request (PR)**. Provide a clear description of your changes and reference any related issues.

## Development Setup

### Prerequisites
*   Go 1.20+
*   SSH access to a test server (optional but recommended for integration testing)

### Building
```bash
go build -o autonfs ./cmd/autonfs
```

### Testing
```bash
# Run unit tests
go test ./...
```

## Code Style
*   We use standard Go formatting (`gofmt`).
*   Comments should be in English.
*   Avoid hardcoded paths or values.

## License
By contributing, you agree that your contributions will be licensed under the AGPLv3 License.
