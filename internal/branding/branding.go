package branding

import (
	"os"
	"path/filepath"
)

const (
	Name                    = "AINexus"
	LegacyName              = "ccNexus"
	DefaultDataDirName      = ".AINexus"
	LegacyDataDirName       = ".ccNexus"
	DefaultDatabaseName     = "ainexus.db"
	LegacyDatabaseFilename  = "ccnexus.db"
	DefaultWebDAVConfigPath = "/AINexus/config"
	DefaultWebDAVStatsPath  = "/AINexus/stats"
)

func LookupEnv(primary, legacy string) string {
	if value := os.Getenv(primary); value != "" {
		return value
	}
	return os.Getenv(legacy)
}

func DefaultDataDir(home string) string {
	return filepath.Join(home, DefaultDataDirName)
}

func LegacyDataDir(home string) string {
	return filepath.Join(home, LegacyDataDirName)
}

func ResolveDataDir(home string) string {
	newDir := DefaultDataDir(home)
	if existsAndAccessible(newDir) {
		return newDir
	}
	legacyDir := LegacyDataDir(home)
	if existsAndAccessible(legacyDir) {
		return legacyDir
	}
	return newDir
}

func ResolveDatabasePath(home, dataDir string) string {
	candidates := []string{
		filepath.Join(dataDir, DefaultDatabaseName),
		filepath.Join(dataDir, LegacyDatabaseFilename),
		filepath.Join(DefaultDataDir(home), DefaultDatabaseName),
		filepath.Join(LegacyDataDir(home), LegacyDatabaseFilename),
	}

	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate
		}
	}

	if dataDir != "" {
		return filepath.Join(dataDir, DefaultDatabaseName)
	}
	if home != "" {
		return filepath.Join(DefaultDataDir(home), DefaultDatabaseName)
	}
	return DefaultDatabaseName
}

func ResolveDatabasePathInDir(dataDir string) string {
	newPath := filepath.Join(dataDir, DefaultDatabaseName)
	if fileExists(newPath) {
		return newPath
	}
	legacyPath := filepath.Join(dataDir, LegacyDatabaseFilename)
	if fileExists(legacyPath) {
		return legacyPath
	}
	return newPath
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func existsAndAccessible(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
