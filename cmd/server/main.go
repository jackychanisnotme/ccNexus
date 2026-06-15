package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/lich0821/ccNexus/internal/branding"
	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/storage"
)

func main() {
	// Parse command line flags
	portFlag := flag.Int("port", 0, "Force specific port (locked, cannot be changed via API)")
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

	deviceID, err := sqliteStorage.GetOrCreateDeviceID()
	if err != nil {
		logger.Warn("Failed to get device ID: %v, using default", err)
		deviceID = "default"
	}

	statsAdapter := storage.NewStatsStorageAdapter(sqliteStorage)
	p := proxy.New(cfg, statsAdapter, sqliteStorage, deviceID)

	// Create HTTP mux
	mux := http.NewServeMux()

	// Initialize and register Web UI (optional plugin)
	// If webui package is not available, this will be skipped at compile time
	if err := registerWebUI(mux, cfg, p, sqliteStorage); err != nil {
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
		logger.Info("Received signal %s, shutting down", sig.String())
		if err := p.Stop(); err != nil {
			logger.Warn("Graceful shutdown failed: %v", err)
		}
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Proxy server stopped with error: %v", err)
			os.Exit(1)
		}
	}

	logger.Info("%s stopped", branding.Name)
}

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
