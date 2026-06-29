package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lich0821/ccNexus/cmd/server/webui/api"
	"github.com/lich0821/ccNexus/internal/branding"
	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/onlinelicense"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/remotelog"
	"github.com/lich0821/ccNexus/internal/service"
	"github.com/lich0821/ccNexus/internal/storage"
)

func main() {
	// Parse command line flags
	portFlag := flag.Int("port", 0, "Force specific port (locked, cannot be changed via API)")
	activateFlag := flag.String("activate", "", "Activate license card and exit")
	licenseStatusFlag := flag.Bool("license-status", false, "Print license status and exit")
	flag.Parse()
	dataDir := resolveDataDir()
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		logger.Error("Failed to create data dir %s: %v", dataDir, err)
		os.Exit(1)
	}

	homeDir, _ := os.UserHomeDir()
	dbPath := branding.LookupEnv("AINEXUS_DB_PATH", "CCNEXUS_DB_PATH")
	if dbPath == "" {
		dbPath = branding.ResolveDatabasePath(homeDir, dataDir)
	}

	sqliteStorage, err := storage.NewSQLiteStorage(dbPath)
	if err != nil {
		logger.Error("Failed to open SQLite storage: %v", err)
		os.Exit(1)
	}
	defer sqliteStorage.Close()

	deviceID, err := sqliteStorage.GetOrCreateDeviceID()
	if err != nil {
		logger.Warn("Failed to get device ID: %v, using default", err)
		deviceID = "default"
	}

	licenseService, licenseErr := onlinelicense.NewConfiguredClientService(sqliteStorage, deviceID, "server")
	if *activateFlag != "" {
		if licenseErr != nil {
			logger.Error("License unavailable: %v", licenseErr)
			os.Exit(1)
		}
		result, err := licenseService.Activate(*activateFlag, timeNow())
		if err != nil {
			logger.Error("License activation failed: %v", err)
			os.Exit(1)
		}
		printJSON(result)
		return
	}
	if *licenseStatusFlag {
		if licenseErr != nil {
			logger.Error("License unavailable: %v", licenseErr)
			os.Exit(1)
		}
		status, err := licenseService.Status(timeNow())
		if err != nil {
			logger.Error("License status failed: %v", err)
			os.Exit(1)
		}
		printJSON(status)
		return
	}

	cfg, err := loadConfig(sqliteStorage)
	if err != nil {
		logger.Error("Unable to load configuration: %v", err)
		os.Exit(1)
	}

	// Handle -port CLI flag (overrides config and locks port)
	if *portFlag > 0 {
		cfg.Port = *portFlag
		cfg.LockPort()
		logger.Info("Port locked to %d via CLI flag", *portFlag)
	}

	if cfg.BasicAuthEnabled && cfg.BasicAuthPassword == "" {
		randomPassword := generateRandomPassword(16)
		cfg.BasicAuthPassword = randomPassword
		logger.Info("======================================")
		logger.Info("  Basic Auth 密码已随机生成")
		logger.Info("  用户名: %s", cfg.BasicAuthUsername)
		logger.Info("  密码: %s", randomPassword)
		logger.Info("  请妥善保存，密码不会再次显示")
		logger.Info("======================================")
		adapter := storage.NewConfigStorageAdapter(sqliteStorage)
		_ = cfg.SaveToStorage(adapter)
	} else if cfg.BasicAuthEnabled {
		logger.Info("Basic Auth 已启用，用户名: %s", cfg.BasicAuthUsername)
	}

	applyEnvOverrides(cfg)
	setLogLevels(cfg.GetLogLevel())

	if err := cfg.Validate(); err != nil {
		logger.Error("Invalid configuration: %v", err)
		os.Exit(1)
	}
	if licenseErr != nil {
		logger.Error("Online license public key unavailable: %v", licenseErr)
		logger.Error("Set CCNEXUS_LICENSE_PUBLIC_KEY and activate online with -activate <cardKey>")
		os.Exit(1)
	}
	if !licenseService.IsLicensed(timeNow()) {
		status, err := licenseService.Status(timeNow())
		if err != nil {
			logger.Error("Unable to read license status: %v", err)
		} else {
			logger.Error("License is not active; starting license-only admin server (status: %s, expiresAt: %s)", status.Message, status.ExpiresAt.Format(time.RFC3339))
		}
		startLicenseOnlyServer(cfg, licenseService, dataDir, dbPath)
		return
	}
	statsAdapter := storage.NewStatsStorageAdapter(sqliteStorage)
	p := proxy.New(cfg, statsAdapter, sqliteStorage, deviceID)
	endpointService := service.NewEndpointService(cfg, p, sqliteStorage)
	licenseService.SetRemoteExecutor(service.NewRemoteManagementExecutor(cfg, sqliteStorage, endpointService))
	licenseService.MaybeRefresh(timeNow())
	remotePollLog := remotelog.NewPollFailureRecorder(serverRemotePollWarnFailures)
	if _, err := licenseService.PollRemoteOnce(); err != nil {
		remotePollLog.Record(err)
	} else {
		remotePollLog.Record(nil)
	}
	ctx, cancelRemote := context.WithCancel(context.Background())
	defer cancelRemote()
	go runRemoteManagementLoop(ctx, licenseService, remotePollLog)

	// Create HTTP mux
	mux := http.NewServeMux()

	// Initialize and register Web UI (optional plugin)
	// If webui package is not available, this will be skipped at compile time
	if err := registerWebUI(mux, cfg, p, sqliteStorage, api.NewLicenseAdapter(licenseService)); err != nil {
		logger.Warn("Web UI not available: %v", err)
	} else {
		logger.Info("Web UI available at /ui/")
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.StartWithMux(mux)
	}()

	logger.Info("%s headless API listening on %s (data dir: %s, db: %s)", branding.Name, cfg.GetListenAddr(), dataDir, dbPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		cancelRemote()
		logger.Info("Received signal %s, shutting down", sig.String())
		if err := p.Stop(); err != nil {
			logger.Warn("Graceful shutdown failed: %v", err)
		}
	case err := <-errCh:
		cancelRemote()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Proxy server stopped with error: %v", err)
			os.Exit(1)
		}
	}

	logger.Info("%s stopped", branding.Name)
}

var timeNow = func() time.Time { return time.Now().UTC() }

const (
	serverRemotePollInterval     = 3 * time.Second
	serverRemotePollMaxBackoff   = 30 * time.Second
	serverRemoteSnapshotInterval = 60 * time.Second
	serverRemotePollWarnFailures = 3
)

func runRemoteManagementLoop(ctx context.Context, licenseService *onlinelicense.ClientService, remotePollLog *remotelog.PollFailureRecorder) {
	if licenseService == nil {
		return
	}
	if remotePollLog == nil {
		remotePollLog = remotelog.NewPollFailureRecorder(serverRemotePollWarnFailures)
	}
	interval := serverRemotePollInterval
	lastSnapshot := time.Now()
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			fullSnapshot := time.Since(lastSnapshot) >= serverRemoteSnapshotInterval
			var outcome *onlinelicense.RemotePollOutcome
			var err error
			if fullSnapshot {
				outcome, err = licenseService.PollRemoteOnce()
			} else {
				outcome, err = licenseService.PollRemoteCommandsOnly()
			}
			if err != nil {
				remotePollLog.Record(err)
				interval = nextServerRemotePollInterval(interval)
			} else {
				remotePollLog.Record(nil)
				interval = serverRemotePollInterval
				if fullSnapshot || (outcome != nil && outcome.SnapshotUpdated) {
					lastSnapshot = time.Now()
				}
			}
			timer.Reset(interval)
		}
	}
}

func nextServerRemotePollInterval(current time.Duration) time.Duration {
	switch {
	case current < 5*time.Second:
		return 5 * time.Second
	case current < 10*time.Second:
		return 10 * time.Second
	default:
		return serverRemotePollMaxBackoff
	}
}

func printJSON(v interface{}) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal JSON: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

func startLicenseOnlyServer(cfg *config.Config, licenseService *onlinelicense.ClientService, dataDir, dbPath string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/license/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeLicenseJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method not allowed"})
			return
		}
		status, err := licenseService.Status(timeNow())
		if err != nil {
			writeLicenseJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}
		writeLicenseJSON(w, http.StatusOK, map[string]interface{}{"success": true, "data": status})
	})
	mux.HandleFunc("/api/license/activate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeLicenseJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method not allowed"})
			return
		}
		var req struct {
			CardKey string `json:"cardKey"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeLicenseJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
			return
		}
		result, err := licenseService.Activate(req.CardKey, timeNow())
		if err != nil {
			writeLicenseJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeLicenseJSON(w, http.StatusOK, map[string]interface{}{"success": true, "data": result})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/ui/" && r.URL.Path != "/admin" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(licenseOnlyHTML))
	})

	server := &http.Server{
		Addr:    cfg.GetListenAddr(),
		Handler: mux,
	}
	logger.Info("%s license-only admin listening on %s (data dir: %s, db: %s)", branding.Name, cfg.GetListenAddr(), dataDir, dbPath)
	logger.Info("Open http://%s/ui/ or activate with: ccnexus-server -activate <cardKey>", cfg.GetListenAddr())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("License-only server stopped with error: %v", err)
		os.Exit(1)
	}
}

func writeLicenseJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

const licenseOnlyHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>AINexus License</title>
  <style>
    body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;margin:0;background:#f6f7fb;color:#1f2937}
    main{max-width:720px;margin:10vh auto;padding:32px;background:#fff;border:1px solid #e5e7eb;border-radius:8px;box-shadow:0 18px 50px rgba(15,23,42,.08)}
    h1{margin:0 0 8px;font-size:24px} p{color:#6b7280;line-height:1.6}
    textarea{box-sizing:border-box;width:100%;min-height:110px;margin:16px 0;padding:12px;border:1px solid #d1d5db;border-radius:6px;font:14px ui-monospace,monospace}
    button{background:#2563eb;color:#fff;border:0;border-radius:6px;padding:10px 16px;font-weight:600;cursor:pointer}
    button.secondary{background:#4b5563}
    #status{margin-top:16px;padding:12px;border-radius:6px;background:#f3f4f6;white-space:pre-wrap}
  </style>
</head>
<body>
  <main>
    <h1>AINexus 授权</h1>
    <p>当前授权未激活或已过期，代理服务未启动。请输入卡密激活/续期；激活成功后重启 server 即可恢复代理服务。</p>
    <textarea id="card" placeholder="请输入授权卡密"></textarea>
    <button onclick="activate()">激活/续期</button>
    <button class="secondary" onclick="status()">刷新状态</button>
    <div id="status">加载中...</div>
  </main>
  <script>
    async function request(path, options){const r=await fetch(path,options);const j=await r.json();if(!r.ok)throw new Error(j.error||'request failed');return j.data||j}
    async function status(){try{const s=await request('/api/license/status');document.getElementById('status').textContent=JSON.stringify(s,null,2)}catch(e){document.getElementById('status').textContent=e.message}}
    async function activate(){try{const cardKey=document.getElementById('card').value.trim();const s=await request('/api/license/activate',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({cardKey})});document.getElementById('status').textContent='激活成功，请重启 server。\n'+JSON.stringify(s,null,2)}catch(e){document.getElementById('status').textContent=e.message}}
    status()
  </script>
</body>
</html>`

func resolveDataDir() string {
	if dir := branding.LookupEnv("AINEXUS_DATA_DIR", "CCNEXUS_DATA_DIR"); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil {
		return resolveDataDirForHome(home)
	}
	return "/data"
}

func resolveDataDirForHome(home string) string {
	return branding.ResolveDataDir(home)
}

func loadConfig(sqliteStorage *storage.SQLiteStorage) (*config.Config, error) {
	adapter := storage.NewConfigStorageAdapter(sqliteStorage)
	cfg, err := config.LoadFromStorage(adapter)
	if err != nil {
		logger.Warn("Failed to load config from storage, using default: %v", err)
		cfg = config.DefaultConfig()
		if saveErr := cfg.SaveToStorage(adapter); saveErr != nil {
			logger.Warn("Failed to persist default config: %v", saveErr)
		}
	}

	// Seed a default endpoint when none are configured to avoid boot failure
	if len(cfg.Endpoints) == 0 {
		logger.Warn("No endpoints found; seeding a default endpoint")
		cfg.Endpoints = config.DefaultConfig().Endpoints
		if saveErr := cfg.SaveToStorage(adapter); saveErr != nil {
			logger.Warn("Failed to persist seeded endpoint: %v", saveErr)
		}
	}
	return cfg, nil
}

func applyEnvOverrides(cfg *config.Config) {
	if portStr := branding.LookupEnv("AINEXUS_PORT", "CCNEXUS_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			cfg.UpdatePort(port)
		} else {
			logger.Warn("Invalid AINEXUS_PORT/CCNEXUS_PORT value %q: %v", portStr, err)
		}
	}

	if levelStr := branding.LookupEnv("AINEXUS_LOG_LEVEL", "CCNEXUS_LOG_LEVEL"); levelStr != "" {
		if level, err := strconv.Atoi(levelStr); err == nil {
			cfg.UpdateLogLevel(level)
		} else {
			logger.Warn("Invalid AINEXUS_LOG_LEVEL/CCNEXUS_LOG_LEVEL value %q: %v", levelStr, err)
		}
	}

	if listenMode := branding.LookupEnv("AINEXUS_LISTEN_MODE", "CCNEXUS_LISTEN_MODE"); listenMode != "" {
		normalized := config.NormalizeListenMode(listenMode)
		if normalized != strings.ToLower(strings.TrimSpace(listenMode)) {
			logger.Warn("Invalid AINEXUS_LISTEN_MODE/CCNEXUS_LISTEN_MODE value %q, using %q", listenMode, normalized)
		}
		cfg.UpdateListenMode(normalized)
	}

	if authEnabled := branding.LookupEnv("AINEXUS_BASIC_AUTH_ENABLED", "CCNEXUS_BASIC_AUTH_ENABLED"); authEnabled != "" {
		enabled := authEnabled == "1" || authEnabled == "true"
		cfg.BasicAuthEnabled = enabled
	}

	if username := branding.LookupEnv("AINEXUS_BASIC_AUTH_USERNAME", "CCNEXUS_BASIC_AUTH_USERNAME"); username != "" {
		cfg.BasicAuthUsername = username
	}

	if password := branding.LookupEnv("AINEXUS_BASIC_AUTH_PASSWORD", "CCNEXUS_BASIC_AUTH_PASSWORD"); password != "" {
		cfg.BasicAuthPassword = password
	}
}

func setLogLevels(level int) {
	if level < 0 {
		return
	}
	logger.GetLogger().SetMinLevel(logger.LogLevel(level))
	logger.GetLogger().SetConsoleLevel(logger.LogLevel(level))
}

func generateRandomPassword(length int) string {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		fallback := make([]byte, length)
		for i := range fallback {
			fallback[i] = byte(i*7%26 + 'a')
		}
		return string(fallback)
	}
	return hex.EncodeToString(bytes)[:length]
}
