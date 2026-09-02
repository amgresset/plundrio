package download

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elsbrock/plundrio/internal/config"
)

// fakeArr mimics the Sonarr/Radarr queue API.
type fakeArr struct {
	records []queueRecord
	deletes []string // "id?query"
	apiKeys []string
}

func (f *fakeArr) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.apiKeys = append(f.apiKeys, r.Header.Get("X-Api-Key"))
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/queue":
			json.NewEncoder(w).Encode(map[string]interface{}{"records": f.records})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v3/queue/"):
			f.deletes = append(f.deletes, strings.TrimPrefix(r.URL.Path, "/api/v3/queue/")+"?"+r.URL.RawQuery)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestBlocklistByHashFindsOwningAppAndUsesBlocklistAndSearch(t *testing.T) {
	sonarr := &fakeArr{records: []queueRecord{{ID: 7, DownloadID: "ABCDEF0123", Title: "Show S01E01", SeriesID: 12}}}
	// radarr shares the download client and lists the same hash as an unknown item (no movieId)
	radarr := &fakeArr{records: []queueRecord{{ID: 3, DownloadID: "ffffffffff", Title: "Movie", MovieID: 5}, {ID: 4, DownloadID: "ABCDEF0123", Title: "Show S01E01"}}}
	s1 := httptest.NewServer(sonarr.handler())
	defer s1.Close()
	s2 := httptest.NewServer(radarr.handler())
	defer s2.Close()

	n := NewArrNotifier([]config.ArrApp{
		{Name: "radarr", URL: s2.URL, APIKey: "rk"},
		{Name: "sonarr", URL: s1.URL, APIKey: "sk"},
	})
	app, err := n.BlocklistByHash(context.Background(), "abcdef0123") // lowercase: put.io hashes are lowercase, *arr stores uppercase
	if err != nil || app != "sonarr" {
		t.Fatalf("expected sonarr to claim the hash, got app=%q err=%v", app, err)
	}
	if len(sonarr.deletes) != 1 {
		t.Fatalf("expected 1 delete on sonarr, got %v", sonarr.deletes)
	}
	d := sonarr.deletes[0]
	for _, want := range []string{"7?", "blocklist=true", "removeFromClient=true", "skipRedownload=false"} {
		if !strings.Contains(d, want) {
			t.Errorf("delete %q missing %q", d, want)
		}
	}
	if len(radarr.deletes) != 0 {
		t.Fatalf("radarr must not be touched, got %v", radarr.deletes)
	}
	if sonarr.apiKeys[0] != "sk" || radarr.apiKeys[0] != "rk" {
		t.Fatalf("api keys not sent per app: %v %v", sonarr.apiKeys, radarr.apiKeys)
	}
}

func TestBlocklistByHashUnknownHashClaimsNothing(t *testing.T) {
	sonarr := &fakeArr{records: []queueRecord{{ID: 7, DownloadID: "ABCDEF0123", SeriesID: 12}}}
	s := httptest.NewServer(sonarr.handler())
	defer s.Close()
	n := NewArrNotifier([]config.ArrApp{{Name: "sonarr", URL: s.URL, APIKey: "sk"}})
	app, err := n.BlocklistByHash(context.Background(), "0000000000")
	if err != nil || app != "" {
		t.Fatalf("expected no claim, got app=%q err=%v", app, err)
	}
	if len(sonarr.deletes) != 0 {
		t.Fatalf("no delete expected, got %v", sonarr.deletes)
	}
}

func TestBlocklistWithRetryWaitsForQueueToShowItem(t *testing.T) {
	sonarr := &fakeArr{} // queue empty at first, like *arr before its next client refresh
	s := httptest.NewServer(sonarr.handler())
	defer s.Close()
	n := NewArrNotifier([]config.ArrApp{{Name: "sonarr", URL: s.URL, APIKey: "sk"}})
	n.attempts, n.interval = 5, 10*time.Millisecond
	go func() {
		time.Sleep(25 * time.Millisecond)
		sonarr.records = []queueRecord{{ID: 9, DownloadID: "abc", SeriesID: 1}}
	}()
	n.BlocklistWithRetry(context.Background(), "abc", "Show")
	if len(sonarr.deletes) != 1 {
		t.Fatalf("expected the retry loop to blocklist once the item appeared, got %v", sonarr.deletes)
	}
}

func TestNewArrNotifierNilWhenUnconfigured(t *testing.T) {
	if NewArrNotifier(nil) != nil {
		t.Fatal("expected nil notifier without apps")
	}
}
