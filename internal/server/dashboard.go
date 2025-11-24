package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/elsbrock/plundrio/internal/download"
)

// DownloadInfo represents a single active download for the dashboard
type DownloadInfo struct {
	Name            string  `json:"name"`
	ProgressPercent float64 `json:"progress_percent"`
	DownloadedMB    float64 `json:"downloaded_mb"`
	TotalMB         float64 `json:"total_mb"`
	SpeedMBps       float64 `json:"speed_mbps"`
	ETA             string  `json:"eta"`
}

// handleDashboardAPI returns active downloads in JSON format
func (s *Server) handleDashboardAPI(w http.ResponseWriter, r *http.Request) {
	coordinator := s.dlManager.GetCoordinator()
	downloads := make([]DownloadInfo, 0)

	// Get all active transfers
	coordinator.GetAllTransfers(func(ctx *download.TransferContext) {
		ctx.Mu.RLock()
		defer ctx.Mu.RUnlock()

		// Only include downloading transfers with progress > 0
		if ctx.State == download.TransferLifecycleDownloading && ctx.TotalSize > 0 {
			downloadedMB := float64(ctx.DownloadedSize) / 1024 / 1024
			totalMB := float64(ctx.TotalSize) / 1024 / 1024
			progressPercent := (float64(ctx.DownloadedSize) / float64(ctx.TotalSize)) * 100

			// Calculate speed and ETA
			speedMBps := 0.0
			eta := "calculating..."

			if !ctx.StartTime.IsZero() && ctx.DownloadedSize > 0 {
				elapsed := time.Since(ctx.StartTime).Seconds()
				if elapsed > 0 {
					speedMBps = downloadedMB / elapsed
					remainingMB := totalMB - downloadedMB
					if speedMBps > 0 {
						etaSeconds := int(remainingMB / speedMBps)
						eta = formatDuration(etaSeconds)
					}
				}
			}

			downloads = append(downloads, DownloadInfo{
				Name:            ctx.Name,
				ProgressPercent: progressPercent,
				DownloadedMB:    downloadedMB,
				TotalMB:         totalMB,
				SpeedMBps:       speedMBps,
				ETA:             eta,
			})
		}
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(downloads)
}

// handleDashboard serves the dashboard HTML
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	html := `<!DOCTYPE html>
<html>
<head>
    <title>Plundrio Dashboard</title>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            background: #0f172a;
            color: #e2e8f0;
            padding: 20px;
        }
        .container { max-width: 1200px; margin: 0 auto; }
        h1 {
            font-size: 2rem;
            margin-bottom: 10px;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
        }
        .header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 20px;
        }
        .active-count {
            background: #1e293b;
            padding: 10px 20px;
            border-radius: 8px;
            border: 1px solid #334155;
            font-size: 0.875rem;
            color: #94a3b8;
        }
        .active-count span {
            color: #667eea;
            font-weight: bold;
            font-size: 1.25rem;
            margin-right: 5px;
        }
        .downloads {
            background: #1e293b;
            border-radius: 10px;
            padding: 20px;
            border: 1px solid #334155;
        }
        .download-item {
            background: #0f172a;
            padding: 15px;
            border-radius: 8px;
            margin-bottom: 15px;
            border: 1px solid #334155;
        }
        .download-name {
            font-weight: 600;
            margin-bottom: 10px;
            color: #f1f5f9;
        }
        .progress-bar {
            background: #334155;
            height: 8px;
            border-radius: 4px;
            overflow: hidden;
            margin: 10px 0;
        }
        .progress-fill {
            background: linear-gradient(90deg, #667eea 0%, #764ba2 100%);
            height: 100%;
            transition: width 0.3s ease;
        }
        .download-stats {
            display: flex;
            justify-content: space-between;
            font-size: 0.875rem;
            color: #94a3b8;
            margin-top: 10px;
        }
        .empty {
            text-align: center;
            padding: 40px;
            color: #64748b;
        }
        .refresh-indicator {
            display: inline-block;
            width: 8px;
            height: 8px;
            background: #10b981;
            border-radius: 50%;
            margin-left: 10px;
            animation: pulse 2s infinite;
        }
        @keyframes pulse {
            0%, 100% { opacity: 1; }
            50% { opacity: 0.5; }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>Plundrio Dashboard <span class="refresh-indicator"></span></h1>
            <div class="active-count">
                <span id="active-count">0</span> active downloads
            </div>
        </div>

        <div class="downloads">
            <div id="downloads-list"></div>
        </div>
    </div>

    <script>
        function formatSize(mb) {
            if (mb >= 1024) {
                return (mb / 1024).toFixed(2) + ' GB';
            }
            return mb.toFixed(2) + ' MB';
        }

        function updateDashboard() {
            fetch('/api/downloads')
                .then(r => r.json())
                .then(downloads => {
                    const list = document.getElementById('downloads-list');

                    if (!downloads || downloads.length === 0) {
                        list.innerHTML = '<div class="empty">No active downloads</div>';
                        document.getElementById('active-count').textContent = '0';
                        return;
                    }

                    list.innerHTML = downloads.map(dl => {
                        return ` + "`" + `
                            <div class="download-item">
                                <div class="download-name">` + "${dl.name}" + `</div>
                                <div class="progress-bar">
                                    <div class="progress-fill" style="width: ` + "${dl.progress_percent}" + `%"></div>
                                </div>
                                <div class="download-stats">
                                    <span>` + "${dl.progress_percent.toFixed(1)}" + `%</span>
                                    <span>` + "${formatSize(dl.downloaded_mb)}" + ` / ` + "${formatSize(dl.total_mb)}" + `</span>
                                    <span>` + "${(dl.speed_mbps || 0).toFixed(1)}" + ` MB/s</span>
                                    <span>ETA: ` + "${dl.eta || 'calculating...'}" + `</span>
                                </div>
                            </div>
                        ` + "`" + `;
                    }).join('');

                    document.getElementById('active-count').textContent = downloads.length;
                });
        }

        // Update every 2 seconds
        updateDashboard();
        setInterval(updateDashboard, 2000);
    </script>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

// formatDuration formats seconds into a human-readable duration
func formatDuration(seconds int) string {
	if seconds < 0 {
		return "unknown"
	}

	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60

	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
