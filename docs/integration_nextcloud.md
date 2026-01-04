# AutoNFS 與 Nextcloud in Docker 整合指南

本指南說明如何將 **AutoNFS** 應用於 **Nextcloud (Docker)** 環境，實現「冷儲存資料的自動喚醒與熱拔插」，並解決 Docker 整合中的常見問題。

## 1. 架構核心 (Architecture)

### 目標場景
將不常存取的大型資料（如影片歸檔）放在 NAS/外部硬碟。Nextcloud 平時不與其連線，僅在使用者點擊資料夾時喚醒硬碟並掛載；閒置時自動關機。

### 關鍵技術要求
1.  **Docker Mount Propagation (`:rslave`)**: 
    讓 Nextcloud Container 能即時看到 Host 端 AutoNFS 的掛載/卸載變更。
2.  **External Storage**: 
    利用 Nextcloud 的 External Storage (Local) 功能映射掛載點。
3.  **Permission (User Mapping)**:
    正確處理 Container 內 `www-data` 使用者與 Host/NFS 權限的對應。

---

## 2. 部署配置 (Configuration)

### 步驟 1: Host 端配置 (AutoNFS)

假設掛載點為 Host 的 `/mnt/nc-archive`.

**`autonfs.yaml`**:
```yaml
hosts:
  - alias: "archive-nas"
    idle_timeout: "30m"     # 建議 > 30m，避免 Web UI 預讀取導致頻繁開關與硬碟損耗
    mounts:
      - local: "/mnt/nc-archive"
        remote: "/volume1/archive"
```
執行 `autonfs apply` 啟用自動掛載。

### 步驟 2: Docker Compose 修改

必須修改 `docker-compose.yaml`，將 Host 掛載點透傳給 Container，並開啟 **Propagation**。

```yaml
services:
  nextcloud:
    image: nextcloud:stable
    environment:
      # [Tuning] 增加 PHP 執行時間，容忍 NAS 喚醒開機 (例如 300秒)
      - PHP_MAX_EXECUTION_TIME=300
    
    volumes:
      # ... 其他 volumes ...
      
      # [重點] 外置硬碟掛載點 + Propagation
      # source: Host 路徑
      # target: Container 內路徑 (建議一致以免混淆)
      # bind.propagation: rslave (關鍵!)
      - type: bind
        source: /mnt/nc-archive
        target: /mnt/nc-archive
        bind:
          propagation: rslave
```

> **語法說明**: 
> *   簡寫: `- /mnt/nc-archive:/mnt/nc-archive:rslave`
> *   **rslave (Recursive Slave)**: Host 端的掛載事件 (Mount/Unmount) 會單向傳播給 Container。若不加此參數，AutoNFS 在 Host 掛載後，Container 內看到的仍是空資料夾。

---

## 3. 權限與隱私 (Permissions & Isolation)

### 權限修正 (Ownership)
Nextcloud Container 內通常以 `www-data` (UID 33) 運行。若 NFS 掛載後的權限為 `root`，Nextcloud 將無法寫入。

*   **影響**: `chown` 操作修改的是**檔案系統的 Metadata**。因為是 NFS，此變更會寫回遠端 NAS。
*   **安全性**: AutoNFS 僅負責網路層與 Systemd Unit 的管理，**不會** 在部署時重置資料夾權限。
*   **操作**:
    您可以放心地在 Host 或 Container 內執行 `chown`：
    ```bash
    # 在 Host 端 (需確認 UID 33 對應 www-data，或直接用 Docker exec)
    docker exec -it nextcloud-app chown -R www-data:www-data /mnt/nc-archive
    ```
    此操作只需執行一次 (除非遠端 NAS 重置了權限)。

### 使用者隔離 (User Isolation) - 推薦做法

若希望實現「每位使用者只能看到自己在 8TB 硬碟中的專屬資料夾」，而不需要為每個人手動設定，可以使用 Nextcloud 的 `$user` 變數功能。

**設定步驟**:
1.  **Host 端準備**:
    *   在 Host 掛載點下，預先建立好使用者目錄 (或透過自動化腳本建立)。
    *   例如：`/mnt/nc-archive/alice`, `/mnt/nc-archive/bob`。
    *   注意：Nextcloud **不會** 自動建立這些目錄，若目錄不存在，該使用者的掛載會顯示錯誤。

2.  **Nextcloud Admin 設定**:
    *   進入 **Administration settings** > **External storage**。
    *   新增一條規則 (適用於 Everyone):
        *   **Folder name**: `Archive` (或 `My Storage`)
        *   **External storage**: `Local`
        *   **Authentication**: `None`
        *   **Configuration**: `/mnt/nc-archive/$user`  <-- **利用變數**
        *   **Available for**: `Everyone` (或特定群組)

**效果**: 
*   使用者 **Alice** 登入時，Nextcloud 會自動嘗試掛載 `/mnt/nc-archive/alice`。
*   使用者 **Bob** 登入時，則掛載 `/mnt/nc-archive/bob`。
*   兩人互不可見，且 Admin 只需要設定一次規則。

**注意事項**:
*   **目錄自動建立**: Nextcloud 目前 **不支援** 自動建立 External Storage 的目標目錄。若目錄不存在，使用者將無法存取。
*   **建議做法**: 若您有大量使用者，可撰寫簡單的 Shell Script 在 Host 端批次建立：
    ```bash
    # 範例：為系統上所有使用者建立歸檔目錄
    for user in alice bob charlie; do
      mkdir -p /mnt/nc-archive/$user
      chown 33:33 /mnt/nc-archive/$user
    done
    ```
*   請確保 `/mnt/nc-archive` 以及子目錄的權限 (`chown -R www-data:www-data`) 正確。

---

## 4. 營運與優化 (Best Practices)

1.  **避免 Wake Storms (背景喚醒風暴)**:
    *   **預覽圖 (Preview)**: Nextcloud 生成縮圖會讀取整個檔案，導致極高 IO。首次瀏覽時會自動喚醒 NAS 並生成縮圖 (存於 AppData)，這是正常現象。
    *   **Cron Jobs**: Nextcloud 預設 Cron Jobs 不會掃描 External Storage。但若安裝了 **Full Text Search** 或 **Antivirus**，請務必將 `/mnt/nc-archive` 加入排除清單，否則 NAS 將無法休眠。
2.  **PHP Timeouts**:
    *   若 NAS 是傳統 HDD Raid，冷開機可能需 60~90 秒。
    *   務必確保 Nginx/Apache 的 `proxy_read_timeout` 與 PHP 的 `max_execution_time` 大於此數值，避免使用者看到 504 錯誤。
