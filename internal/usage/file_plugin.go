// Package usage provides file-based persistence for usage statistics.
// It periodically saves in-memory statistics to disk and restores them on startup.
package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

// FileUsagePlugin persists usage statistics to a JSON file.
// It periodically saves data and restores on startup.
type FileUsagePlugin struct {
	filePath     string
	interval     time.Duration
	stats        *RequestStatistics
	mu           sync.RWMutex
	stopCh       chan struct{}
	stopped      bool
	restoreOnStart bool
}

// FileUsageData wraps StatisticsSnapshot with metadata.
type FileUsageData struct {
	Version  int                   `json:"version"`
	SavedAt  time.Time             `json:"saved_at"`
	Data     StatisticsSnapshot    `json:"data"`
}

// NewFileUsagePlugin creates a new file persistence plugin.
//
// Parameters:
//   - filePath: Path to the JSON file (supports ~ for home directory)
//   - interval: Auto-save interval
//   - restore: Whether to restore data from file on startup
//   - stats: The RequestStatistics instance to persist
//
// Returns:
//   - *FileUsagePlugin: A new plugin instance
func NewFileUsagePlugin(filePath string, interval time.Duration, restore bool, stats *RequestStatistics) *FileUsagePlugin {
	resolvedPath := resolvePath(filePath)
	return &FileUsagePlugin{
		filePath:       resolvedPath,
		interval:       interval,
		stats:          stats,
		stopCh:         make(chan struct{}),
		restoreOnStart: restore,
	}
}

// HandleUsage implements coreusage.Plugin (no-op, data comes from shared stats).
func (p *FileUsagePlugin) HandleUsage(ctx context.Context, record coreusage.Record) {
	// No-op: LoggerPlugin handles aggregation, we just persist periodically
}

// Start begins the background save goroutine.
func (p *FileUsagePlugin) Start() {
	if p.restoreOnStart {
		if err := p.Load(); err != nil {
			log.Warnf("usage: failed to restore from %s: %v", p.filePath, err)
		}
	}

	if p.interval <= 0 {
		return
	}

	go p.run()
}

// Stop stops the background goroutine and performs a final save.
func (p *FileUsagePlugin) Stop() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	p.mu.Unlock()

	close(p.stopCh)

	// Final save on shutdown
	if err := p.Save(); err != nil {
		log.Errorf("usage: final save failed: %v", err)
	}
}

func (p *FileUsagePlugin) run() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := p.Save(); err != nil {
				log.Errorf("usage: periodic save failed: %v", err)
			}
		case <-p.stopCh:
			return
		}
	}
}

// Save writes current statistics to file atomically.
func (p *FileUsagePlugin) Save() error {
	if p.stats == nil {
		return nil
	}

	p.mu.RLock()
	path := p.filePath
	p.mu.RUnlock()

	// Get current snapshot
	snapshot := p.stats.Snapshot()

	data := FileUsageData{
		Version: 1,
		SavedAt: time.Now().UTC(),
		Data:    snapshot,
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal usage data: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	// Atomic write: write to temp file then rename
	tempFile := path + ".tmp"
	if err := os.WriteFile(tempFile, jsonData, 0o600); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tempFile, path); err != nil {
		// Clean up temp file on failure
		os.Remove(tempFile)
		return fmt.Errorf("rename temp file: %w", err)
	}

	log.Debugf("usage: saved statistics to %s (requests=%d, tokens=%d)",
		path, snapshot.TotalRequests, snapshot.TotalTokens)
	return nil
}

// Load restores statistics from file.
func (p *FileUsagePlugin) Load() error {
	p.mu.RLock()
	path := p.filePath
	p.mu.RUnlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No existing file, start fresh
		}
		return fmt.Errorf("read file %s: %w", path, err)
	}

	var fileData FileUsageData
	if err := json.Unmarshal(data, &fileData); err != nil {
		// Backup corrupt file
		backupPath := path + ".corrupt." + time.Now().Format("20060102-150405")
		if renameErr := os.Rename(path, backupPath); renameErr == nil {
			log.Warnf("usage: corrupt file backed up to %s", backupPath)
		}
		return fmt.Errorf("unmarshal usage data: %w", err)
	}

	if fileData.Version != 1 {
		return fmt.Errorf("unsupported version: %d", fileData.Version)
	}

	// Merge loaded data into current stats
	result := p.stats.MergeSnapshot(fileData.Data)
	log.Infof("usage: restored from %s (added=%d, skipped=%d)",
		path, result.Added, result.Skipped)

	return nil
}

// resolvePath expands ~ to home directory.
func resolvePath(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		remainder := strings.TrimPrefix(path, "~")
		remainder = strings.TrimLeft(remainder, "/\\")
		if remainder == "" {
			return home
		}
		return filepath.Join(home, remainder)
	}
	return path
}
