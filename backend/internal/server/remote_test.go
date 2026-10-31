package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/storage"
)

// fakeRemote keeps blobs in memory; fail makes every call fail as an
// unavailable service would.
type fakeRemote struct {
	mu      sync.Mutex
	blobs   map[string][]byte
	fail    bool
	deletes int
}

func (f *fakeRemote) Name() string             { return "filebin" }
func (f *fakeRemote) MaxExpiry() time.Duration { return 6 * 24 * time.Hour }

func (f *fakeRemote) Upload(_ context.Context, ref string, r io.Reader, size int64, sum [32]byte) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return storage.ErrUnavailable
	}
	if int64(len(data)) != size || sha256.Sum256(data) != sum {
		return fmt.Errorf("%w: checksum", storage.ErrUnavailable)
	}
	f.blobs[ref] = data
	return nil
}

func (f *fakeRemote) Download(_ context.Context, ref string, size int64) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, storage.ErrUnavailable
	}
	data, ok := f.blobs[ref]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeRemote) Delete(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return storage.ErrUnavailable
	}
	delete(f.blobs, ref)
	f.deletes++
	return nil
}

func (f *fakeRemote) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.blobs)
}

// withRemote offers a fake "filebin" that passed its health check.
func withRemote(t *testing.T, e *testEnv) *fakeRemote {
	t.Helper()
	fake := &fakeRemote{blobs: map[string][]byte{}}
	e.srv.addRemote(fake)
	if err := e.srv.remotes["filebin"].check(t.Context(), e.clock); err != nil {
		t.Fatal(err)
	}
	return fake
}

func TestRemoteUploadDownload(t *testing.T) {
	e := newTestEnv(t)
	fake := withRemote(t, e)
	up := e.upload(t, "ciphertext", "?storage=filebin&max_downloads=1")
	if fake.count() != 1 {
		t.Fatalf("%d blobs at the service, want 1", fake.count())
	}
	// Nothing stays on this server's disk.
	if total, _ := e.srv.store.TotalSize(t.Context()); total != 0 {
		t.Errorf("local total %d, want 0", total)
	}

	rec := e.do("GET", "/api/v1/files/"+up.ID, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ciphertext" {
		t.Fatalf("download: %d %q", rec.Code, rec.Body)
	}
	if rec.Header().Get("Content-Length") != "10" || rec.Header().Get("Downloads-Remaining") != "0" {
		t.Errorf("headers %v", rec.Header())
	}
	// The last download deletes the blob right after it finished.
	if fake.count() != 0 {
		t.Errorf("blob still at the service after the last download")
	}
	if rec := e.do("GET", "/api/v1/files/"+up.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("second download: %d", rec.Code)
	}
}

func TestRemoteChunkedUpload(t *testing.T) {
	e := newTestEnv(t)
	fake := withRemote(t, e)
	rec := e.do("POST", "/api/v1/uploads?storage=filebin", nil)
	var cu createUploadResponse
	json.Unmarshal(rec.Body.Bytes(), &cu)
	auth := "Bearer " + cu.UploadToken
	for i, part := range []string{"cipher", "text"} {
		offset := []string{"0", "6"}[i]
		if rec := e.do("PUT", "/api/v1/uploads/"+cu.ID+"?offset="+offset, strings.NewReader(part), "Authorization", auth); rec.Code != http.StatusOK {
			t.Fatalf("chunk %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	rec = e.do("POST", "/api/v1/uploads/"+cu.ID+"/complete", nil, "Authorization", auth)
	if rec.Code != http.StatusCreated {
		t.Fatalf("complete: %d %s", rec.Code, rec.Body)
	}
	var up uploadResponse
	json.Unmarshal(rec.Body.Bytes(), &up)
	if fake.count() != 1 {
		t.Fatalf("%d blobs at the service", fake.count())
	}
	if rec := e.do("GET", "/api/v1/files/"+up.ID, nil); rec.Body.String() != "ciphertext" {
		t.Errorf("download %q", rec.Body)
	}
}

func TestRemoteRefusedBeforeData(t *testing.T) {
	e := newTestEnv(t)
	if rec := e.do("POST", "/api/v1/files?storage=filebin", strings.NewReader("x")); rec.Code != http.StatusBadRequest {
		t.Errorf("not offered: %d, want 400", rec.Code)
	}
	fake := withRemote(t, e)
	if rec := e.do("POST", "/api/v1/files?storage=filebin&expires=168h", strings.NewReader("x")); rec.Code != http.StatusBadRequest {
		t.Errorf("expiry beyond the service's: %d, want 400", rec.Code)
	}
	// A failed health check takes it out of use.
	fake.fail = true
	e.srv.remotes["filebin"].check(t.Context(), e.clock)
	for _, target := range []string{"/api/v1/files?storage=filebin", "/api/v1/uploads?storage=filebin"} {
		if rec := e.do("POST", target, strings.NewReader("x")); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s while down: %d, want 503", target, rec.Code)
		}
	}
	// Local storage keeps working.
	e.upload(t, "x", "?storage=local")
}

// Real transfers that fail in a row take the service out of use, without
// waiting 12 hours for the next check.
func TestRemotePassiveHealth(t *testing.T) {
	e := newTestEnv(t)
	fake := withRemote(t, e)
	ups := []uploadResponse{}
	for range maxFailures {
		ups = append(ups, e.upload(t, "x", "?storage=filebin"))
	}
	fake.fail = true
	for i, up := range ups {
		rec := e.do("GET", "/api/v1/files/"+up.ID, nil)
		if rec.Code != http.StatusBadGateway {
			t.Errorf("download %d: %d, want 502", i, rec.Code)
		}
	}
	if healthy, _ := e.srv.remotes["filebin"].status(); healthy {
		t.Error("still offered after repeated failures")
	}
	// Downloads of other files are now refused before they are counted.
	fake.fail = false
	up := ups[0]
	if rec := e.do("GET", "/api/v1/files/"+up.ID, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("download while out of use: %d, want 503", rec.Code)
	}
}

func TestRemoteDailyCap(t *testing.T) {
	e := newTestEnv(t)
	withRemote(t, e)
	e.srv.cfg.ProxyDailyBytes = 15
	up := e.upload(t, "ciphertext", "?storage=filebin") // 10 bytes
	if rec := e.do("POST", "/api/v1/files?storage=filebin", strings.NewReader("more data")); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("over the cap: %d, want 503", rec.Code)
	}
	// The counter starts again the next day (UTC); the clock is at noon.
	e.clock = e.clock.Add(12 * time.Hour)
	if rec := e.do("GET", "/api/v1/files/"+up.ID, nil); rec.Code != http.StatusOK {
		t.Errorf("next day: %d", rec.Code)
	}
}

func TestRemoteDeleteAndExpiry(t *testing.T) {
	e := newTestEnv(t)
	fake := withRemote(t, e)
	a := e.upload(t, "deleted", "?storage=filebin")
	e.upload(t, "expires", "?storage=filebin&expires=1h")

	if rec := e.do("DELETE", "/api/v1/files/"+a.ID, nil, "Authorization", "Bearer "+a.DeleteToken); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	e.clock = e.clock.Add(time.Hour)
	if _, err := e.srv.store.DeleteExpired(t.Context(), e.clock); err != nil {
		t.Fatal(err)
	}
	// A failing service keeps the entries for the next round.
	fake.fail = true
	e.srv.deleteDueRemote(t.Context())
	if fake.count() != 2 {
		t.Fatalf("%d blobs, want both still there", fake.count())
	}
	fake.fail = false
	e.srv.deleteDueRemote(t.Context())
	if fake.count() != 0 {
		t.Errorf("%d blobs left at the service", fake.count())
	}
	if due, _ := e.srv.store.RemoteDue(t.Context(), e.clock); len(due) != 0 {
		t.Errorf("still due: %v", due)
	}
}

func TestStorageStatus(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do("GET", "/api/v1/storage", nil)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"storage":[]}` {
		t.Errorf("none offered: %d %s", rec.Code, rec.Body)
	}
	withRemote(t, e)
	rec = e.do("GET", "/api/v1/storage", nil)
	var resp struct{ Storage []storageStatus }
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Storage) != 1 || resp.Storage[0].Name != "filebin" || !resp.Storage[0].Available ||
		resp.Storage[0].MaxExpirySeconds != int64(6*24*time.Hour/time.Second) || !resp.Storage[0].CheckedAt.Equal(e.clock) {
		t.Errorf("status %s", rec.Body)
	}
}

// The health check uses random bytes and cleans up after itself.
func TestRemoteCheck(t *testing.T) {
	e := newTestEnv(t)
	fake := withRemote(t, e)
	if fake.count() != 0 || fake.deletes != 1 {
		t.Errorf("check left %d blobs, %d deletes", fake.count(), fake.deletes)
	}
	fake.fail = true
	if err := e.srv.remotes["filebin"].check(t.Context(), e.clock); !errors.Is(err, storage.ErrUnavailable) {
		t.Errorf("check: %v", err)
	}
}

// An upload link can send what it receives on to a service: single and
// chunked uploads go there, the owner fetches through this server as often
// as needed, and revoking the link deletes the blobs.
func TestRemoteInbox(t *testing.T) {
	e := newTestEnv(t)
	if rec := e.do("POST", "/api/v1/inboxes?storage=filebin", strings.NewReader(testPublicKey)); rec.Code != http.StatusBadRequest {
		t.Errorf("link to a service not offered: %d, want 400", rec.Code)
	}
	fake := withRemote(t, e)
	in := e.createInbox(t, "?storage=filebin")
	if in.Storage != "filebin" || in.KeepSeconds != int64(6*24*time.Hour/time.Second) {
		t.Errorf("created %+v", in)
	}
	if rec := e.do("GET", "/api/v1/inboxes/"+in.ID, nil); !strings.Contains(rec.Body.String(), `"storage":"filebin"`) {
		t.Errorf("info does not tell the uploader: %s", rec.Body)
	}

	if rec := e.do("POST", "/api/v1/inboxes/"+in.ID+"/files", strings.NewReader("ciphertext")); rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	up := e.createUpload(t, "?inbox="+in.ID)
	e.chunk(up, 0, "chunked")
	if rec := e.complete(up); rec.Code != http.StatusCreated {
		t.Fatalf("complete: %d %s", rec.Code, rec.Body)
	}
	if fake.count() != 2 {
		t.Fatalf("%d blobs at the service, want 2", fake.count())
	}
	if total, _ := e.srv.store.TotalSize(t.Context()); total != 0 || e.srv.reserved != 0 {
		t.Errorf("local total %d, reserved %d, want 0", total, e.srv.reserved)
	}

	var list manageInboxResponse
	json.Unmarshal(e.manage("GET", "/files", in).Body.Bytes(), &list)
	if len(list.Files) != 2 || list.Files[0].ExpiresAt != e.clock.Add(6*24*time.Hour).UTC().Truncate(time.Second) {
		t.Fatalf("list %+v", list)
	}
	first := list.Files[0]
	if first.Size != 10 { // same second: the order is by ID
		first = list.Files[1]
	}
	for range 2 {
		if rec := e.manage("GET", "/files/"+first.ID, in); rec.Code != http.StatusOK || rec.Body.String() != "ciphertext" {
			t.Fatalf("owner download: %d %q", rec.Code, rec.Body)
		}
	}

	// Down: refused before the data, for both kinds of upload.
	fake.fail = true
	e.srv.remotes["filebin"].check(t.Context(), e.clock)
	for _, target := range []string{"/api/v1/inboxes/" + in.ID + "/files", "/api/v1/uploads?inbox=" + in.ID} {
		if rec := e.do("POST", target, strings.NewReader("x")); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s while down: %d, want 503", target, rec.Code)
		}
	}
	if rec := e.do("POST", "/api/v1/inboxes?storage=filebin", strings.NewReader(testPublicKey)); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("new link while down: %d, want 503", rec.Code)
	}
	fake.fail = false
	e.srv.remotes["filebin"].check(t.Context(), e.clock)

	if rec := e.manage("DELETE", "", in); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rec.Code)
	}
	e.srv.deleteDueRemote(t.Context())
	if fake.count() != 0 {
		t.Errorf("%d blobs left after revoking", fake.count())
	}
}

// A link that fills up while its upload is on the way to the service
// leaves nothing behind there.
func TestRemoteInboxFullAfterSend(t *testing.T) {
	e := newTestEnv(t)
	fake := withRemote(t, e)
	in := e.createInbox(t, "?storage=filebin&max_uploads=1")
	a := e.createUpload(t, "?inbox="+in.ID)
	b := e.createUpload(t, "?inbox="+in.ID)
	e.chunk(a, 0, "one")
	e.chunk(b, 0, "two")
	if rec := e.complete(a); rec.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}
	if rec := e.complete(b); rec.Code != http.StatusConflict {
		t.Errorf("second: %d, want 409", rec.Code)
	}
	if fake.count() != 1 {
		t.Errorf("%d blobs at the service, want 1", fake.count())
	}
}
