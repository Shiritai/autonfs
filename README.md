**AutoNFS** 是一個針對「偶爾存取」的 NAS/Server 設計的智慧型 NFS 掛載管理工具。它結合了 **Wake-on-LAN (WoL)**、**Systemd Automount** 與 **Advanced Idle Watcher**，實現「要用時自動喚醒掛載，不用時自動斷線關機」的極致節能體驗。

---

## 🔥 特色 (Features)

*   **⚡️ 按需喚醒 (On-Demand Wake)**
    當您存取掛載點 (如 `ls /mnt/nas`) 時，Master 端會自動發送 WoL 魔術封包喚醒 Slave，並等待 NFS 服務就緒後才完成掛載。完全透明，無需手動執行指令。

*   **🧠 智慧監控 (Smart Watcher)**
    告別傳統不穩定的 TCP 連線偵測。AutoNFS 採用 **多重訊號聚合 (Multi-Source Signal Aggregation)** 技術來精準判定系統狀態：
    1.  **NFSv4 Clients (黃金標準)**: 直接讀取 Kernel `/proc/fs/nfsd/clients/`，只要有 Client 掛載，絕不關機。
    2.  **RPC Operations**: 監控 NFS 操作流量，確保高負載傳輸時不中斷。
    3.  **System Load**: 系統負載過高時自動延後關機。

*   **🛡️ 自動部署 (Atomic Deployment)**
    單一 Binary 包含 Master/Slave 所有邏輯。`deploy` 指令會透過 SSH 自動完成所有配置 (Systemd Unit, NFS Exports, Watcher Service)，並確保原子性更新。

---

## 🚀 快速開始 (Quick Start)

### 1. 安裝 (Installation)

需要 Go 1.20+ 環境：

```bash
# 編譯
go build -o autonfs ./cmd/autonfs
```

### 2. 部署 (Deployment)

將本地 Master 的 `/mnt/nas` 掛載到遠端 Slave 的 `/data/files`。

```bash
# 語法: autonfs deploy [ssh_alias] [options]
./autonfs deploy myserver \
  --local-dir /mnt/nas \
  --remote-dir /data/files \
  --idle 30m \
  --watcher-dry-run  # 建議初次部屬先開啟 DryRun 測試
```

*   `myserver`: 您的 SSH config alias (或 `user@ip`)。
*   `--idle`: 設定閒置多久後關機 (Master 會先斷線，Slave 接著關機)。
*   `--watcher-dry-run`: 測試模式，Slave 時間到只會寫 Log 不會真關機。

### 3. 反部署 (Undeploy)

若要移除設定或發生錯誤：

```bash
# 同時清理本地與遠端 (推薦)
./autonfs undeploy --local-dir /mnt/nas --remote myserver

# 只清理本地
./autonfs undeploy --local-dir /mnt/nas
```

---

## 🛠️ 架構原理解析 (Architecture)

### 生命周期 (Lifecycle)

1.  **Idle (初始狀態)**:
    *   Slave: 關機中。
    *   Master: `automount` 服務監聽 `/mnt/nas`。
2.  **Access (存取)**:
    *   User 執行 `ls /mnt/nas`。
    *   Master 核發 WoL 喚醒 Slave，等待 Port 2049 開啟。
    *   NFS 掛載成功。
3.  **Active (活躍)**:
    *   Slave 的 `autonfs-watcher` 偵測到 Client 連線 (`/proc/fs/nfsd/clients` 有資料)。
    *   Slave 保持開機。
4.  **Disconnect (斷線)**:
    *   User 停止操作。
    *   Master 等待 `IdleTimeout` (如 30m) 後，Systemd 自動執行 `umount`。
5.  **Shutdown (關機)**:
    *   Slave Watcher 發現 Client 消失且無流量。
    *   Slave 開始倒數 `IdleTimeout`。
    *   時間到 -> 執行 `systemctl poweroff`。

### Watcher 狀態監控

您可以透過 SSH 到 Slave 查看即時監控日誌：

```bash
journalctl -f -u autonfs-watcher
```

日誌範例：
```
[ACTIVE] Client Connected (192.168.1.100) | Load: 0.15 | Ops: 52
[IDLE]   Dataset: 0 clients, 0 ops        | Load: 0.05 | Idle: 5m (Shutdown in 25m)
```

---

## ⚠️ 常見問題 (Troubleshooting)

### Q: 為什麼 Master 沒有自動 Unmount？
**A:** 請檢查您是否還停留在掛載目錄內 (Shell `cd /mnt/nas`)。請執行 `cd ~` 離開該目錄，否則掛載點會被佔用導致無法卸載。

### Q: 部署後 Slave 一直沒有關機？
**A:**
1.  檢查 Master 是否已經 Unmount (`mount | grep nfs`)。
2.  檢查 Slave 日誌 (`journalctl -u autonfs-watcher`)，確認是否有其他 Clients 或高負載。
3.  確認是否開啟了 `--watcher-dry-run`。

### Q: 部署失敗 "File not found" 或 "Permission denied"？
**A:** 請確認 SSH 使用者有 `sudo` 權限。AutoNFS 部署時需要 sudo 來寫入 `/etc/systemd/system` 與 `/etc/exports.d`。

---

## License
MIT
