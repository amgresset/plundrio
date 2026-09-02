package config

// DownloadStartWindowConfig gates when new local downloads may begin.
// It only affects the start of local downloads, not ongoing transfers.
type DownloadStartWindowConfig struct {
	Enabled bool
	Start   string
	End     string
}

// ArrApp is a Sonarr/Radarr-style application that uses plundrio as its
// download client. Used to blocklist fake releases through the app's own API.
type ArrApp struct {
	Name   string
	URL    string // e.g. http://192.168.0.20:8989
	APIKey string
}

// Config holds the runtime configuration
type Config struct {
	// TargetDir is where completed downloads will be stored
	TargetDir string

	// PutioFolder is the name of the folder in Put.io
	PutioFolder string

	// FolderID is the Put.io folder ID (set after creation/lookup)
	FolderID int64

	// OAuthToken is the Put.io OAuth token
	OAuthToken string

	// ListenAddr is the address to listen for transmission-rpc requests
	ListenAddr string

	// WorkerCount is the number of concurrent download workers (default: 4)
	WorkerCount int

	// DownloadStartWindow optionally restricts when new local downloads may start.
	DownloadStartWindow DownloadStartWindowConfig

	// ArrApps are the *arr applications to notify when a transfer turns out to
	// be a fake release (no media files, only executables).
	ArrApps []ArrApp
}
