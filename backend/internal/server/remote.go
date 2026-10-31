package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/storage"
	"github.com/FileBunny/filebunny/backend/internal/store"
)

// External storage ("remotes"): the server sends ciphertext on to a
// service and fetches it back, so the service only sees this server.
const (
	// maxRemoteTransfers caps the transfers to and from one service in
	// progress, server-wide.
	maxRemoteTransfers = 8
	// remoteTransferTimeout bounds one upload or download to a service.
	remoteTransferTimeout = 15 * time.Minute
	// healthInterval is how often each service is tested; more often
	// could look like abuse to the service.
	healthInterval = 12 * time.Hour
	// healthProbeSize is the size of the random test blob.
	healthProbeSize = 1024
	// maxFailures real transfers failing in a row take a service out of
	// use until the next successful health check.
	maxFailures = 3
	// trashInterval is how often blobs due for deletion are deleted.
	trashInterval = 5 * time.Minute
)

var (
	errRemoteDown = errors.New("external storage unavailable")
	errRemoteBusy = errors.New("external storage busy")
)

// remote is a service with its health and traffic state.
type remote struct {
	storage.Remote
	slots chan struct{}

	mu        sync.Mutex
	healthy   bool
	checkedAt time.Time
	failures  int
	// day and dayBytes count the traffic of the current UTC day.
	day      time.Time
	dayBytes int64
}

func newRemote(r storage.Remote) *remote {
	return &remote{Remote: r, slots: make(chan struct{}, maxRemoteTransfers)}
}

// status reports whether the service is offered and when it was checked.
func (r *remote) status() (healthy bool, checkedAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.healthy, r.checkedAt
}

// acquire claims a transfer slot and n bytes of today's traffic. The
// caller must call release when the transfer is done.
func (r *remote) acquire(n, dailyCap int64, now time.Time) (release func(), err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.healthy {
		return nil, errRemoteDown
	}
	if day := now.UTC().Truncate(24 * time.Hour); !day.Equal(r.day) {
		r.day, r.dayBytes = day, 0
	}
	if r.dayBytes+n > dailyCap {
		return nil, errRemoteBusy
	}
	select {
	case r.slots <- struct{}{}:
	default:
		return nil, errRemoteBusy
	}
	r.dayBytes += n
	return func() { <-r.slots }, nil
}

// report records the outcome of a real transfer: a few failures in a row
// take the service out of use, a success puts it back.
func (r *remote) report(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case err == nil:
		r.healthy, r.failures = true, 0
	case errors.Is(err, storage.ErrUnavailable):
		r.failures++
		if r.failures >= maxFailures {
			r.healthy = false
		}
	}
}

// check tests the service with a random blob: upload, download, compare,
// delete. The blob is random bytes, never user content.
func (r *remote) check(ctx context.Context, now time.Time) error {
	probe := make([]byte, healthProbeSize)
	rand.Read(probe)
	ref := storage.NewRef()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	err := r.Upload(ctx, ref, bytes.NewReader(probe), int64(len(probe)), sha256.Sum256(probe))
	if err == nil {
		var rc io.ReadCloser
		if rc, err = r.Download(ctx, ref, int64(len(probe))); err == nil {
			var got []byte
			got, err = io.ReadAll(rc)
			rc.Close()
			if err == nil && !bytes.Equal(got, probe) {
				err = errors.New("downloaded blob differs")
			}
		}
		// Delete whatever happened; Filebin expires it anyway.
		if derr := r.Delete(ctx, ref); err == nil {
			err = derr
		}
	}
	r.mu.Lock()
	r.healthy, r.checkedAt, r.failures = err == nil, now, 0
	r.mu.Unlock()
	return err
}

// RunBackground checks the services and deletes remote blobs that are due,
// until ctx is cancelled. It returns at once if no service is configured.
func (s *Server) RunBackground(ctx context.Context) {
	if len(s.remotes) == 0 {
		return
	}
	health := time.NewTicker(healthInterval)
	defer health.Stop()
	trash := time.NewTicker(trashInterval)
	defer trash.Stop()
	s.checkRemotes(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-health.C:
			s.checkRemotes(ctx)
		case <-trash.C:
			s.deleteDueRemote(ctx)
		}
	}
}

func (s *Server) checkRemotes(ctx context.Context) {
	for _, r := range s.remotes {
		if err := r.check(ctx, s.now()); err != nil && ctx.Err() == nil {
			s.log.Warn("external storage check failed", "storage", r.Name(), "err", err)
		}
	}
}

// deleteDueRemote deletes the remote blobs whose files were removed. A
// failure leaves the entry for the next round.
func (s *Server) deleteDueRemote(ctx context.Context) {
	due, err := s.store.RemoteDue(ctx, s.now())
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("listing remote deletions failed", "err", err)
		}
		return
	}
	for _, o := range due {
		r := s.remotes[o.Storage]
		if r == nil {
			continue // service switched off; the blob expires there
		}
		dctx, cancel := context.WithTimeout(ctx, time.Minute)
		err := r.Delete(dctx, o.Ref)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn("remote delete failed", "storage", o.Storage, "err", err)
			}
			continue
		}
		if err := s.store.RemoteDeleted(ctx, o); err != nil && ctx.Err() == nil {
			s.log.Error("recording remote delete failed", "err", err)
		}
	}
}

// commitRemote sends a received upload on to the service in lim and records
// it; p is aborted afterwards in every case.
func (s *Server) commitRemote(ctx context.Context, p *store.Pending, lim limits) (store.File, string, error) {
	defer p.Abort()
	r := lim.remote
	release, err := r.acquire(p.Size(), s.cfg.ProxyDailyBytes, s.now())
	if err != nil {
		return store.File{}, "", err
	}
	defer release()

	content, err := p.Content()
	if err != nil {
		return store.File{}, "", err
	}
	h := sha256.New()
	if _, err := io.Copy(h, content); err != nil {
		return store.File{}, "", err
	}
	if _, err := content.Seek(0, io.SeekStart); err != nil {
		return store.File{}, "", err
	}
	var sum [32]byte
	h.Sum(sum[:0])

	ref := storage.NewRef()
	tctx, cancel := context.WithTimeout(ctx, remoteTransferTimeout)
	defer cancel()
	err = r.Upload(tctx, ref, content, p.Size(), sum)
	r.report(err)
	if err != nil {
		return store.File{}, "", errors.Join(errRemoteDown, err)
	}
	now := s.now()
	f, token, err := s.store.CommitRemote(ctx, p.Size(), r.Name(), ref, now, now.Add(lim.expiry), lim.maxDownloads)
	if err != nil {
		dctx, dcancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		_ = r.Delete(dctx, ref)
		dcancel()
		return store.File{}, "", err
	}
	return f, token, nil
}

// addRemote offers r as external storage.
func (s *Server) addRemote(r storage.Remote) {
	s.remotes[r.Name()] = newRemote(r)
}

// putRemote receives an upload body into a temp file and sends it on.
func (s *Server) putRemote(ctx context.Context, body io.Reader, lim limits) (store.File, string, error) {
	p, err := s.store.Begin()
	if err != nil {
		return store.File{}, "", err
	}
	if _, err := p.Append(body); err != nil {
		p.Abort()
		return store.File{}, "", err
	}
	return s.commitRemote(ctx, p, lim)
}

type storageStatus struct {
	Name      string    `json:"name"`
	Available bool      `json:"available"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	// MaxExpirySeconds is the longest expiry an upload there may ask for.
	MaxExpirySeconds int64 `json:"max_expiry_seconds"`
}

// handleStorage lists the external services this server offers.
func (s *Server) handleStorage(w http.ResponseWriter, _ *http.Request) {
	list := []storageStatus{}
	for _, r := range s.remotes {
		healthy, checked := r.status()
		st := storageStatus{Name: r.Name(), Available: healthy,
			MaxExpirySeconds: int64(min(s.cfg.MaxExpiry, r.MaxExpiry()) / time.Second)}
		if !checked.IsZero() {
			st.CheckedAt = checked.UTC()
		}
		list = append(list, st)
	}
	slices.SortFunc(list, func(a, b storageStatus) int { return strings.Compare(a.Name, b.Name) })
	writeJSON(w, http.StatusOK, map[string]any{"storage": list})
}

// writeRemoteError answers for errRemoteDown and errRemoteBusy; it reports
// whether err was one of them.
func writeRemoteError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errRemoteBusy):
		w.Header().Set("Retry-After", "600")
		writeError(w, http.StatusServiceUnavailable, "external storage is busy, try again later or store on this server")
	case errors.Is(err, errRemoteDown):
		w.Header().Set("Retry-After", "3600")
		writeError(w, http.StatusServiceUnavailable, "external storage is unavailable, store on this server instead")
	default:
		return false
	}
	return true
}

// serveRemote streams a file's blob from its service. The caller has
// claimed a transfer and counted the download.
func (s *Server) serveRemote(w http.ResponseWriter, r *http.Request, f store.File, rem *remote) {
	ctx, cancel := context.WithTimeout(r.Context(), remoteTransferTimeout)
	defer cancel()
	blob, err := rem.Download(ctx, f.RemoteRef, f.Size)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	rem.report(err)
	if err != nil {
		s.log.Warn("remote download failed", "storage", f.Storage, "err", err)
		writeError(w, http.StatusBadGateway, "external storage failed, try again later")
		return
	}
	defer blob.Close()

	setFileHeaders(w, f)
	sw := stallWriter{w, http.NewResponseController(w), s.stall}
	if _, err := io.Copy(sw, blob); err != nil {
		if r.Context().Err() == nil {
			s.log.Warn("remote download interrupted", "err", err)
		}
		return
	}
	if f.DownloadsLeft() == 0 {
		// The last download is done: delete now instead of after the grace
		// period.
		o := store.RemoteObject{Storage: f.Storage, Ref: f.RemoteRef}
		dctx, dcancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
		defer dcancel()
		if rem.Delete(dctx, o.Ref) == nil {
			_ = s.store.RemoteDeleted(dctx, o)
		}
	}
}
