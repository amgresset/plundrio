package download

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/elsbrock/plundrio/internal/config"
	"github.com/elsbrock/plundrio/internal/log"
)

// ArrNotifier asks Sonarr/Radarr-style apps to blocklist a download by its
// info hash, using the same "blocklist and search" action the UI offers.
type ArrNotifier struct {
	apps   []config.ArrApp
	client *http.Client
	// retry cadence; overridable in tests
	attempts int
	interval time.Duration
}

// NewArrNotifier returns nil when no apps are configured.
func NewArrNotifier(apps []config.ArrApp) *ArrNotifier {
	if len(apps) == 0 {
		return nil
	}
	return &ArrNotifier{
		apps:     apps,
		client:   &http.Client{Timeout: 30 * time.Second},
		attempts: 12,
		interval: 20 * time.Second,
	}
}

type queueRecord struct {
	ID         int64  `json:"id"`
	DownloadID string `json:"downloadId"`
	Title      string `json:"title"`
	SeriesID   int64  `json:"seriesId"` // Sonarr
	MovieID    int64  `json:"movieId"`  // Radarr
}

// owned reports whether the app mapped this queue item to one of its own
// series/movies. Apps sharing a download client also list each other's
// downloads as "unknown" items; those must not be claimed.
func (r queueRecord) owned() bool { return r.SeriesID > 0 || r.MovieID > 0 }

// BlocklistByHash tries each app once. It returns the name of the app that
// owned the download, or "" if none of them had it in their queue.
func (n *ArrNotifier) BlocklistByHash(ctx context.Context, hash string) (string, error) {
	var errs []string
	for _, app := range n.apps {
		ids, err := n.findQueueIDs(ctx, app, hash)
		if err != nil {
			errs = append(errs, app.Name+": "+err.Error())
			continue
		}
		if len(ids) == 0 {
			continue
		}
		for _, id := range ids {
			if err := n.blocklistQueueItem(ctx, app, id); err != nil {
				errs = append(errs, app.Name+": "+err.Error())
				continue
			}
		}
		return app.Name, nil
	}
	if len(errs) > 0 {
		return "", fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return "", nil
}

// BlocklistWithRetry keeps trying until an app claims the hash or the attempts
// run out. The *arr app only lists the download in its queue after its next
// refresh of the download client, which can lag our detection by a minute.
func (n *ArrNotifier) BlocklistWithRetry(ctx context.Context, hash, name string) {
	for i := 1; i <= n.attempts; i++ {
		app, err := n.BlocklistByHash(ctx, hash)
		if app != "" {
			log.Info("arr").Str("app", app).Str("hash", hash).Str("name", name).Int("attempt", i).
				Msg("Fake release blocklisted via *arr; it will remove the transfer and search again")
			return
		}
		if err != nil {
			log.Warn("arr").Str("hash", hash).Int("attempt", i).Err(err).Msg("Error asking *arr apps to blocklist")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(n.interval):
		}
	}
	log.Warn("arr").Str("hash", hash).Str("name", name).
		Msg("No *arr app claimed this fake release; leaving it on put.io with an error for manual cleanup")
}

func (n *ArrNotifier) findQueueIDs(ctx context.Context, app config.ArrApp, hash string) ([]int64, error) {
	u := app.URL + "/api/v3/queue?pageSize=1000"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("X-Api-Key", app.APIKey)
	resp, err := n.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("queue: HTTP %d", resp.StatusCode)
	}
	var page struct {
		Records []queueRecord `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("queue: %w", err)
	}
	var ids []int64
	for _, r := range page.Records {
		if r.owned() && strings.EqualFold(r.DownloadID, hash) {
			ids = append(ids, r.ID)
		}
	}
	return ids, nil
}

func (n *ArrNotifier) blocklistQueueItem(ctx context.Context, app config.ArrApp, id int64) error {
	q := url.Values{
		"removeFromClient": {"true"},
		"blocklist":        {"true"},
		"skipRedownload":   {"false"},
	}
	u := fmt.Sprintf("%s/api/v3/queue/%d?%s", app.URL, id, q.Encode())
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	req.Header.Set("X-Api-Key", app.APIKey)
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("queue delete %d: HTTP %d", id, resp.StatusCode)
	}
	return nil
}
