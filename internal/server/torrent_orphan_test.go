package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elsbrock/go-putio"
	"github.com/elsbrock/plundrio/internal/config"
	"github.com/elsbrock/plundrio/internal/download"
)

// stubDL is a DownloadService backed by plain maps.
type stubDL struct {
	transfers []*putio.Transfer
	ctxs      map[int64]*download.TransferContext
	removed   []int64
}

func (d *stubDL) GetTransfers() []*putio.Transfer { return d.transfers }
func (d *stubDL) GetTransferContext(id int64) (*download.TransferContext, bool) {
	c, ok := d.ctxs[id]
	return c, ok
}
func (d *stubDL) GetAllTransfers(fn func(*download.TransferContext)) {
	for _, c := range d.ctxs {
		fn(c)
	}
}
func (d *stubDL) FindTransferContextByHash(hash string) (*download.TransferContext, bool) {
	for _, c := range d.ctxs {
		if c.Hash == hash {
			return c, true
		}
	}
	return nil, false
}
func (d *stubDL) RemoveTransferContext(id int64) {
	d.removed = append(d.removed, id)
	delete(d.ctxs, id)
}
func (d *stubDL) SetCategory(string, string) {}
func (d *stubDL) GetCategory(string) string  { return "" }
func (d *stubDL) RemoveCategory(string)      {}
func (d *stubDL) Stop()                      {}

// stubPutio records which put.io calls the server makes.
type stubPutio struct {
	transfers        []*putio.Transfer
	deletedTransfers []int64
	deletedFiles     []int64
}

func (p *stubPutio) GetAccountInfo(context.Context) (*putio.AccountInfo, error) { return nil, nil }
func (p *stubPutio) GetTransfers(context.Context) ([]*putio.Transfer, error)    { return p.transfers, nil }
func (p *stubPutio) UploadFile(context.Context, []byte, string, int64) (string, error) {
	return "", nil
}
func (p *stubPutio) AddTransfer(context.Context, string, int64) (string, error) { return "", nil }
func (p *stubPutio) DeleteFile(_ context.Context, id int64) error {
	p.deletedFiles = append(p.deletedFiles, id)
	return nil
}
func (p *stubPutio) DeleteTransfer(_ context.Context, id int64) error {
	p.deletedTransfers = append(p.deletedTransfers, id)
	return nil
}

func newTestServer(t *testing.T, pc *stubPutio, dl *stubDL) *Server {
	t.Helper()
	return &Server{
		cfg:       &config.Config{TargetDir: t.TempDir()},
		client:    pc,
		dlService: dl,
	}
}

func trackedTransfer(id int64, name, hash string, state download.TransferLifecycleState) *download.TransferContext {
	ctx := download.NewTransferContext(id, 10, state)
	ctx.Name = name
	ctx.Hash = hash
	ctx.FileID = 500
	ctx.SetTotalSize(1000)
	ctx.AddDownloadedBytes(400)
	return ctx
}

func torrentGet(t *testing.T, s *Server, args string) []map[string]interface{} {
	t.Helper()
	res, err := s.handleTorrentGet(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("torrent-get: %v", err)
	}
	return res.(map[string]interface{})["torrents"].([]map[string]interface{})
}

func TestTorrentGetReportsTransferMissingOnPutio(t *testing.T) {
	dl := &stubDL{ctxs: map[int64]*download.TransferContext{
		7: trackedTransfer(7, "Show S01", "abc123", download.TransferLifecycleDownloading),
	}}
	s := newTestServer(t, &stubPutio{}, dl)

	got := torrentGet(t, s, `{}`)
	if len(got) != 1 {
		t.Fatalf("expected 1 torrent from local tracking, got %d", len(got))
	}
	if got[0]["hashString"] != "abc123" || got[0]["name"] != "Show S01" {
		t.Fatalf("unexpected torrent: %v", got[0])
	}
	if got[0]["status"] != trStatusDownload {
		t.Fatalf("expected downloading status, got %v", got[0]["status"])
	}
	// put.io half done + 40% of local half
	if pd := got[0]["percentDone"].(float64); pd < 0.69 || pd > 0.71 {
		t.Fatalf("expected ~0.70 progress, got %v", pd)
	}
}

func TestTorrentGetDoesNotDuplicateListedTransfers(t *testing.T) {
	ctx := trackedTransfer(7, "Show S01", "abc123", download.TransferLifecycleDownloading)
	dl := &stubDL{
		transfers: []*putio.Transfer{{ID: 7, Hash: "abc123", Name: "Show S01", Status: "COMPLETED", PercentDone: 100, Size: 1000}},
		ctxs:      map[int64]*download.TransferContext{7: ctx},
	}
	s := newTestServer(t, &stubPutio{}, dl)

	if got := torrentGet(t, s, `{}`); len(got) != 1 {
		t.Fatalf("expected exactly 1 torrent, got %d", len(got))
	}
}

func TestTorrentGetSkipsUntrackedHashesAndHonoursIDs(t *testing.T) {
	dl := &stubDL{ctxs: map[int64]*download.TransferContext{
		1: trackedTransfer(1, "A", "aaa", download.TransferLifecycleDownloading),
		2: trackedTransfer(2, "B", "bbb", download.TransferLifecycleProcessed),
		3: trackedTransfer(3, "no hash", "", download.TransferLifecycleDownloading),
	}}
	s := newTestServer(t, &stubPutio{}, dl)

	if got := torrentGet(t, s, `{}`); len(got) != 2 {
		t.Fatalf("expected 2 torrents (hashless one skipped), got %d", len(got))
	}
	got := torrentGet(t, s, `{"ids":["BBB"]}`)
	if len(got) != 1 || got[0]["hashString"] != "bbb" {
		t.Fatalf("expected only bbb (case-insensitive), got %v", got)
	}
	if got[0]["status"] != trStatusStopped || got[0]["percentDone"] != 1.0 {
		t.Fatalf("processed transfer should report stopped+complete, got %v", got[0])
	}
}

func TestTorrentRemoveOfTransferMissingOnPutio(t *testing.T) {
	pc := &stubPutio{}
	dl := &stubDL{ctxs: map[int64]*download.TransferContext{
		7: trackedTransfer(7, "Show S01", "abc123", download.TransferLifecycleProcessed),
	}}
	s := newTestServer(t, pc, dl)
	local := filepath.Join(s.cfg.TargetDir, "Show S01")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := s.handleTorrentRemove(context.Background(), json.RawMessage(`{"ids":["abc123"],"delete-local-data":true}`)); err != nil {
		t.Fatalf("torrent-remove: %v", err)
	}

	if len(dl.removed) != 1 || dl.removed[0] != 7 {
		t.Fatalf("expected local tracking removed for 7, got %v", dl.removed)
	}
	if _, err := os.Stat(local); !os.IsNotExist(err) {
		t.Fatalf("expected local data deleted, stat err=%v", err)
	}
	if len(pc.deletedTransfers) != 0 {
		t.Fatalf("should not call put.io DeleteTransfer for a transfer it no longer has, got %v", pc.deletedTransfers)
	}
	if len(pc.deletedFiles) != 0 {
		t.Fatalf("processed transfer's source is already gone; should not DeleteFile, got %v", pc.deletedFiles)
	}
	if got := torrentGet(t, s, `{}`); len(got) != 0 {
		t.Fatalf("removed transfer must drop out of torrent-get, got %d", len(got))
	}
}

func TestTorrentRemoveOfMidDownloadOrphanDeletesRemoteFile(t *testing.T) {
	pc := &stubPutio{}
	dl := &stubDL{ctxs: map[int64]*download.TransferContext{
		7: trackedTransfer(7, "Show S01", "abc123", download.TransferLifecycleDownloading),
	}}
	s := newTestServer(t, pc, dl)

	if _, err := s.handleTorrentRemove(context.Background(), json.RawMessage(`{"ids":["abc123"]}`)); err != nil {
		t.Fatalf("torrent-remove: %v", err)
	}
	if len(pc.deletedFiles) != 1 || pc.deletedFiles[0] != 500 {
		t.Fatalf("expected source file 500 deleted on put.io, got %v", pc.deletedFiles)
	}
	if len(dl.removed) != 1 {
		t.Fatalf("expected local tracking removed, got %v", dl.removed)
	}
}

func TestTorrentRemoveOfListedTransferAlsoDropsLocalTracking(t *testing.T) {
	pc := &stubPutio{transfers: []*putio.Transfer{{ID: 7, Hash: "abc123", Name: "Show S01", FileID: 500}}}
	dl := &stubDL{
		transfers: pc.transfers,
		ctxs:      map[int64]*download.TransferContext{7: trackedTransfer(7, "Show S01", "abc123", download.TransferLifecycleProcessed)},
	}
	s := newTestServer(t, pc, dl)

	if _, err := s.handleTorrentRemove(context.Background(), json.RawMessage(`{"ids":["abc123"]}`)); err != nil {
		t.Fatalf("torrent-remove: %v", err)
	}
	if len(pc.deletedTransfers) != 1 || pc.deletedTransfers[0] != 7 {
		t.Fatalf("expected put.io transfer 7 deleted, got %v", pc.deletedTransfers)
	}
	if len(dl.removed) != 1 || dl.removed[0] != 7 {
		t.Fatalf("expected local tracking dropped too, got %v", dl.removed)
	}
	// put.io's list refreshes on the next poll; the local context must be
	// gone too, otherwise the orphan path would resurrect the transfer.
	dl.transfers = nil
	if got := torrentGet(t, s, `{}`); len(got) != 0 {
		t.Fatalf("after removal nothing should be listed, got %d", len(got))
	}
}

func TestTorrentGetReportsFakeReleaseAsError(t *testing.T) {
	tc := download.NewTransferCoordinator(func(int64) {})
	tc.InitiateTransfer(7, "Show S01E01", "abc123", 500, 0)
	if err := tc.FailTransfer(7, &download.FakeReleaseError{Detail: "only executable \"Show S01E01.exe\""}); err != nil {
		t.Fatal(err)
	}
	ctx, _ := tc.GetTransferContext(7)
	dl := &stubDL{
		transfers: []*putio.Transfer{{ID: 7, Hash: "abc123", Name: "Show S01E01", Status: "COMPLETED", PercentDone: 100, Size: 1000}},
		ctxs:      map[int64]*download.TransferContext{7: ctx},
	}
	s := newTestServer(t, &stubPutio{}, dl)
	got := torrentGet(t, s, `{}`)
	if len(got) != 1 || got[0]["error"] != true {
		t.Fatalf("expected an errored torrent, got %v", got)
	}
	if es, _ := got[0]["errorString"].(string); !strings.Contains(es, "fake release") {
		t.Fatalf("errorString should explain the refusal, got %q", es)
	}
}
