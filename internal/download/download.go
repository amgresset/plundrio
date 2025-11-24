package download

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/elsbrock/plundrio/internal/log"
)

// downloadWorker processes download jobs from the queue
func (m *Manager) downloadWorker() {
	for {
		select {
		case <-m.stopChan:
			// Immediate shutdown requested
			log.Info("download").Msg("Worker stopping due to shutdown request")
			return
		case job, ok := <-m.jobs:
			if !ok {
				return
			}
			state := &DownloadState{
				FileID:     job.FileID,
				Name:       job.Name,
				TransferID: job.TransferID,
				StartTime:  time.Now(),
			}
			err := m.downloadWithRetry(state)
			if err != nil {
				if downloadErr, ok := err.(*DownloadError); ok && downloadErr.Type == "DownloadCancelled" {
					log.Info("download").
						Str("file_name", job.Name).
						Msg("Download cancelled due to shutdown")
					// Just remove from active files for cancelled downloads
					m.activeFiles.Delete(job.FileID)
					// Don't call FailTransfer for cancellations
					continue
				}
				// Handle permanent failures
				log.Error("download").
					Str("file_name", job.Name).
					Err(err).
					Msg("Failed to download file")

				// Just remove the file from active files but don't fail the entire transfer
				// We'll keep the transfer context so we can retry later
				m.activeFiles.Delete(job.FileID)

				// Mark this file as failed in the transfer context
				m.handleFileFailure(job.TransferID)
				continue
			}
			// Pass both transferID and fileID to handleFileCompletion
			// The file cleanup is now handled inside handleFileCompletion
			m.handleFileCompletion(job.TransferID, job.FileID)
			// Do NOT call m.activeFiles.Delete here - now handled in handleFileCompletion
		}
	}
}

// downloadWithRetry attempts to download a file with retries on transient errors
func (m *Manager) downloadWithRetry(state *DownloadState) error {
	const maxRetries = 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if err := m.downloadFile(state); err != nil {
			// Check for cancellation first - pass it through without wrapping
			if downloadErr, ok := err.(*DownloadError); ok && downloadErr.Type == "DownloadCancelled" {
				return err
			}

			lastErr = err
			if !isTransientError(err) {
				return fmt.Errorf("permanent error on attempt %d: %w", attempt, err)
			}
			log.Warn("download").
				Str("file_name", state.Name).
				Int("attempt", attempt).
				Err(err).
				Msg("Retrying download after error")
			time.Sleep(time.Second * time.Duration(attempt))
			continue
		}
		return nil
	}
	return fmt.Errorf("failed after %d attempts, last error: %w", maxRetries, lastErr)
}

// isTransientError determines if an error is potentially recoverable
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Check for cancellation errors - these should be passed through
	if downloadErr, ok := err.(*DownloadError); ok && downloadErr.Type == "DownloadCancelled" {
		return false
	}

	// Check for grab errors
	if err.Error() == "connection reset" ||
		err.Error() == "connection refused" ||
		err.Error() == "i/o timeout" {
		return true
	}

	// Check for specific grab HTTP errors
	if strings.Contains(err.Error(), "429") || // Too Many Requests
		strings.Contains(err.Error(), "503") || // Service Unavailable
		strings.Contains(err.Error(), "504") || // Gateway Timeout
		strings.Contains(err.Error(), "502") { // Bad Gateway
		return true
	}

	return false
}

// downloadFile downloads a file from Put.io using aria2c for multi-connection downloads
func (m *Manager) downloadFile(state *DownloadState) error {
	// Create a context that's cancelled when stopChan is closed
	ctx, cancel := context.WithCancel(context.Background())

	// Set up cancellation from stopChan
	go func() {
		select {
		case <-m.stopChan:
			cancel()
		case <-ctx.Done():
		}
	}()

	defer cancel()

	// Get download URL
	url, err := m.client.GetDownloadURL(state.FileID)
	if err != nil {
		return fmt.Errorf("failed to get download URL: %w", err)
	}

	// Prepare target path
	targetPath := filepath.Join(m.cfg.TargetDir, state.Name)
	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Check if file exists from previous non-aria2c download
	// If it does and there's no .aria2 control file, remove it
	if _, err := os.Stat(targetPath); err == nil {
		aria2ControlFile := targetPath + ".aria2"
		if _, err := os.Stat(aria2ControlFile); os.IsNotExist(err) {
			// File exists but not from aria2c, remove it so aria2c can start fresh
			log.Info("download").
				Str("file_name", state.Name).
				Msg("Removing existing partial download from previous session")
			if err := os.Remove(targetPath); err != nil {
				log.Warn("download").
					Str("file_name", state.Name).
					Err(err).
					Msg("Failed to remove existing file, continuing anyway")
			}
		}
	}

	// aria2c arguments for maximum speed
	args := []string{
		"-x", "16", // 16 connections per server
		"-s", "16", // Split file into 16 segments
		"-k", "1M", // Min split size 1MB
		"--max-tries=5",
		"--retry-wait=3",
		"--connect-timeout=30",
		"--timeout=60",
		"--allow-overwrite=true",
		"--auto-file-renaming=false",
		"--continue=true", // Resume support
		"--summary-interval=0", // Disable summary to reduce output
		"--console-log-level=notice", // Reduce console spam
		"-d", targetDir,
		"-o", filepath.Base(targetPath),
		url,
	}

	log.Info("download").
		Str("file_name", state.Name).
		Str("target_path", targetPath).
		Msg("Starting download with aria2c (16 connections)")

	// Create aria2c command
	cmd := exec.CommandContext(ctx, "aria2c", args...)

	// Get stdout pipe for progress tracking
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Get stderr pipe
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start aria2c: %w", err)
	}

	// Monitor progress in goroutine
	progressDone := make(chan struct{})
	go m.monitorAria2cProgress(ctx, state, stdout, stderr, progressDone)

	// Wait for command to complete
	cmdErr := cmd.Wait()

	// Signal progress monitor to stop
	close(progressDone)

	// Check for cancellation
	if ctx.Err() != nil {
		return NewDownloadCancelledError(state.Name, "download stopped")
	}

	// Check for command errors
	if cmdErr != nil {
		return fmt.Errorf("aria2c failed: %w", cmdErr)
	}

	// Verify file exists and get size
	fileInfo, err := os.Stat(targetPath)
	if err != nil {
		return fmt.Errorf("failed to verify downloaded file: %w", err)
	}

	totalSize := fileInfo.Size()
	elapsed := time.Since(state.StartTime).Seconds()
	averageSpeedMBps := (float64(totalSize) / 1024 / 1024) / elapsed

	// Update transfer context with the completed file size
	if transferCtx, exists := m.coordinator.GetTransferContext(state.TransferID); exists {
		transferCtx.DownloadedSize += totalSize

		log.Debug("download").
			Str("file_name", state.Name).
			Int64("transfer_id", state.TransferID).
			Int64("file_size", totalSize).
			Int64("transfer_downloaded", transferCtx.DownloadedSize).
			Int64("transfer_total", transferCtx.TotalSize).
			Msg("Updated transfer with completed file size")
	}

	log.Info("download").
		Str("file_name", state.Name).
		Float64("size_mb", float64(totalSize)/1024/1024).
		Float64("speed_mbps", averageSpeedMBps).
		Dur("duration", time.Since(state.StartTime)).
		Str("target_path", targetPath).
		Msg("Download completed with aria2c")

	return nil
}

// monitorAria2cProgress monitors aria2c output for progress updates
func (m *Manager) monitorAria2cProgress(ctx context.Context, state *DownloadState, stdout, stderr io.ReadCloser, done chan struct{}) {
	// Regex to parse aria2c progress output
	// Example: [#1 SIZE:1.2GiB/10.5GiB(11%) CN:16 DL:45.2MiB ETA:3m12s]
	progressRegex := regexp.MustCompile(`\[#\d+.*?(\d+)%.*?DL:([\d.]+)(KiB|MiB|GiB).*?ETA:([^\]]+)\]`)

	scanner := bufio.NewScanner(io.MultiReader(stdout, stderr))
	lastProgress := float64(0)
	lastLogTime := time.Now()

	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		default:
			if scanner.Scan() {
				line := scanner.Text()

				// Parse progress line
				matches := progressRegex.FindStringSubmatch(line)
				if len(matches) >= 5 {
					progress, _ := strconv.ParseFloat(matches[1], 64)
					speed, _ := strconv.ParseFloat(matches[2], 64)
					speedUnit := matches[3]
					eta := matches[4]

					// Convert speed to MB/s
					speedMBps := speed
					switch speedUnit {
					case "KiB":
						speedMBps = speed / 1024
					case "GiB":
						speedMBps = speed * 1024
					}

					// Update state
					state.mu.Lock()
					state.Progress = progress
					state.downloaded = int64(progress) // Approximate
					state.LastProgress = time.Now()
					state.mu.Unlock()

					// Log progress every 5 seconds
					if time.Since(lastLogTime) >= m.dlConfig.ProgressUpdateInterval && progress != lastProgress {
						log.Info("download").
							Str("file_name", state.Name).
							Float64("progress_percent", progress).
							Float64("speed_mbps", speedMBps).
							Str("eta", eta).
							Msg("Download progress")

						lastProgress = progress
						lastLogTime = time.Now()
					}
				} else if strings.Contains(line, "Exception") || strings.Contains(line, "error") || strings.Contains(line, "ERROR") || strings.Contains(line, "failed") {
					// Log aria2c error messages
					log.Error("download").
						Str("file_name", state.Name).
						Str("aria2c_output", line).
						Msg("aria2c error output")
				}
			}
		}
	}
}
