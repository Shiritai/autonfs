# AutoNFS

[中文版 (Chinese Version)](README_zh-TW.md)

> **Intelligent On-Demand NFS Mount Manager** for Home Labs & Energy Saving.
> Wake-on-LAN on Access. Auto-Shutdown on Idle. Zero Configuration.

[![License: AGPL v3](https://img.shields.io/badge/License-AGPL%20v3-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/archroiko/autonfs)](https://goreportcard.com/report/github.com/archroiko/autonfs)

**AutoNFS** is a modern, single-binary replacement for complex `autofs` + scripts setups. It automatically manages the full lifecycle of your remote NAS/Server connection:

1.  **Wake-on-Demand**: Sends Magic Packet (WoL) instantly when you access the mount point.
2.  **Wait-for-Service**: Blocks access until the NFS server is actually ready (preventing timeouts).
3.  **Smart Monitoring**: Precision kernel-level monitoring (`/proc/fs/nfsd/clients`) ensures the server *never* sleeps while you are watching a movie or transferring files.
4.  **Auto-Shutdown**: Powers off the server when truly idle to save energy.

---

## 🔥 Features

*   **⚡️ Zero-Config Deployment**:
    Forget about editing `/etc/auto.master`, `/etc/exports`, or writing custom systemd service files. `autonfs apply` deploys everything (Client Units, Server Watcher, NFS Exports) via SSH in seconds.
*   **🧠 Kernel-Level Accuracy**:
    Unlike scripts that rely on TCP ping or generic load averages, AutoNFS's Watcher inspects the Linux Kernel NFSd state directly. It knows exactly when a client is connected.
*   **🛡️ Atomic & Declarative**:
    Define your infrastructure in `autonfs.yaml`. The deployment is idempotent and self-healing.

---

## 🚀 Quick Start

### 1. Installation

#### Option A: Download Binary (Recommended)
Download the latest version (`v0.0.1+`) for your platform from [GitHub Releases](https://github.com/archroiko/autonfs/releases).

#### Option B: Build from Source
Requires Go 1.20+:

```bash
go build -ldflags="-s -w" -o autonfs ./cmd/autonfs
```

### 2. Configuration (`autonfs.yaml`)

Create a `autonfs.yaml` file to define your setup:

```yaml
hosts:
  - alias: "my-nas"   # SSH Configuration Alias
    idle_timeout: "30m"       # Shutdown after 30m inactivity
    wake_timeout: "120s"      # Wait max 120s for boot
    shutdown_cmd: "systemctl poweroff" # Custom shutdown command (Optional)
    mounts:
      - local: "/mnt/archive"
        remote: "/volume1/archive"
        mount_options: "rw,soft,timeo=100" # Custom mount options (Optional)
```

### 3. Deploy

**Dry Run** (Preview changes):
```bash
./autonfs apply -f autonfs.yaml --dry-run
```

**Apply** (Deploy to both local client and remote server):
```bash
./autonfs apply -f autonfs.yaml
```

*   **Idempotency**: Safe to run multiple times. It only updates changed units.
*   **Self-Healing**: Automatically ensures services are enabled and running.

### 4. Undeploy

To remove all configurations and services:

```bash
./autonfs undeploy --local-dir /mnt/archive --remote my-nas
```

### 5. Check Version

```bash
./autonfs version
# Output: AutoNFS v0.0.1 (b7c96f1)
```

---

## 🛠️ Architecture

### Workflow

```mermaid
sequenceDiagram
    participant User
    participant C as Client (Master)
    participant S as Server (Slave)

    Note over C: Idle (Unmounted)
    User->>C: Access /mnt/archive
    C->>S: Send WoL Magic Packet
    Note over S: Booting...
    S-->>C: Port 2049 Active
    C->>S: NFS Mount Request
    S-->>C: Mount Success
    User->>C: Read/Write Files
    
    loop Watcher Monitoring
        Note over S: Check /proc/fs/nfsd/clients
        Note over S: Active Client = Keep Awake
    end

    User->>C: Stop Access
    Note over C: Idle Timeout (e.g. 30m)
    C->>S: Unmount
    
    Note over S: Client Disconnected
    Note over S: Watcher: Idle Countdown
    Note over S: Watcher: Poweroff
```

### Watcher State Machine

The watcher runs as a systemd service on the remote server side.

```mermaid
stateDiagram-v2
    [*] --> Monitoring
    Monitoring --> Active: Client Connected / High Load
    Active --> Monitoring: Client Disconnected / Low Load
    
    Monitoring --> IdleCountdown: No Clients & Low Load
    IdleCountdown --> Monitoring: Activity Detected
    IdleCountdown --> Shutdown: Timeout Reached
    
    Shutdown --> [*]: System Poweroff
```

---

## 🧩 Integrations

*   **Nextcloud in Docker**: Use AutoNFS to provide on-demand "Cold Storage" for Nextcloud. See [Nextcloud Integration Guide](docs/integration_nextcloud.md).

## 📜 License

This project is licensed under the **GNU Affero General Public License v3.0 (AGPLv3)**.
See [LICENSE](LICENSE) for details.

---

## ⚠️ Troubleshooting

### Q: Why doesn't the Master unmount automatically?
**A:** Check if you are still inside the mount directory (e.g., shell `cd /mnt/nas`). Please execute `cd ~` to leave the directory, otherwise the mount point will be busy and cannot be unmounted.

### Q: The Slave doesn't shut down after deployment?
**A:**
1.  Check if the Master has unmounted (`mount | grep nfs`).
2.  Check the Slave logs (`journalctl -u autonfs-watcher`) to confirm if there are other clients or high load.
3.  Confirm if `--watcher-dry-run` was enabled.

### Q: Deployment fails with "File not found" or "Permission denied"?
**A:** Please ensure the SSH user has `sudo` privileges. AutoNFS requires sudo to write to `/etc/systemd/system` and `/etc/exports.d` during deployment.
