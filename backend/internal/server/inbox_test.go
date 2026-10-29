package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testPublicKey = "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nmDMEauD1WRYJKwYBBAHaRw8BAQdA\n=IFdm\n-----END PGP PUBLIC KEY BLOCK-----\n"

func (e *testEnv) createInbox(t *testing.T, query string) createInboxResponse {
	t.Helper()
	rec := e.do("POST", "/api/v1/inboxes"+query, strings.NewReader(testPublicKey))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create upload link: status %d: %s", rec.Code, rec.Body)
	}
	var resp createInboxResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func (e *testEnv) manage(method, path string, in createInboxResponse) *httptest.ResponseRecorder {
	return e.do(method, "/api/v1/inboxes/"+in.ID+path, nil, "Authorization", "Bearer "+in.ManageToken)
}

func TestCreateInbox(t *testing.T) {
	e := newTestEnv(t)
	in := e.createInbox(t, "")
	if in.ID == "" || len(in.ManageToken) != 43 || in.MaxUploads != 10 || in.MaxSize != 100<<20 {
		t.Errorf("response %+v", in)
	}
	if want := e.clock.Add(7 * 24 * time.Hour); !in.ExpiresAt.Equal(want) {
		t.Errorf("expires_at %v, want %v", in.ExpiresAt, want)
	}
	in = e.createInbox(t, "?expires=720h&max_uploads=100")
	if in.MaxUploads != 100 || !in.ExpiresAt.Equal(e.clock.Add(720*time.Hour)) {
		t.Errorf("response %+v", in)
	}

	for name, c := range map[string]struct {
		query, body string
		status      int
	}{
		"not a key":         {"", "hello", http.StatusBadRequest},
		"private key":       {"", "-----BEGIN PGP PRIVATE KEY BLOCK-----\nx\n-----END PGP PRIVATE KEY BLOCK-----", http.StatusBadRequest},
		"binary in armor":   {"", "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\x00\x01\n-----END PGP PUBLIC KEY BLOCK-----", http.StatusBadRequest},
		"html":              {"", "-----BEGIN PGP PUBLIC KEY BLOCK-----<script>-----END PGP PUBLIC KEY BLOCK-----", http.StatusCreated},
		"too large":         {"", strings.Repeat("a", maxPublicKeySize+1), http.StatusRequestEntityTooLarge},
		"expiry too long":   {"?expires=721h", testPublicKey, http.StatusBadRequest},
		"expiry too short":  {"?expires=30s", testPublicKey, http.StatusBadRequest},
		"too many uploads":  {"?max_uploads=101", testPublicKey, http.StatusBadRequest},
		"zero uploads":      {"?max_uploads=0", testPublicKey, http.StatusBadRequest},
		"uploads not a num": {"?max_uploads=ten", testPublicKey, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			rec := e.do("POST", "/api/v1/inboxes"+c.query, strings.NewReader(c.body))
			if rec.Code != c.status {
				t.Errorf("status %d, want %d: %s", rec.Code, c.status, rec.Body)
			}
		})
	}
}

// The key is served as JSON, never as a page: the HTML-looking key above
// cannot run anywhere.
func TestInboxInfo(t *testing.T) {
	e := newTestEnv(t)
	in := e.createInbox(t, "?max_uploads=2")
	rec := e.do("GET", "/api/v1/inboxes/"+in.ID, nil)
	var info inboxInfoResponse
	json.Unmarshal(rec.Body.Bytes(), &info)
	if rec.Code != http.StatusOK || info.PublicKey != testPublicKey || info.UploadsLeft != 2 || info.MaxFileSize != testMaxSize {
		t.Fatalf("status %d: %+v", rec.Code, info)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q", ct)
	}
	if rec := e.do("GET", "/api/v1/inboxes/unknown", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown: status %d", rec.Code)
	}
	e.clock = e.clock.Add(8 * 24 * time.Hour)
	if rec := e.do("GET", "/api/v1/inboxes/"+in.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("expired: status %d", rec.Code)
	}
}

func TestInboxUploadAndManage(t *testing.T) {
	e := newTestEnv(t)
	in := e.createInbox(t, "")

	rec := e.do("POST", "/api/v1/inboxes/"+in.ID+"/files", strings.NewReader("ciphertext one"))
	if rec.Code != http.StatusCreated || strings.TrimSpace(rec.Body.String()) != `{"size":14}` {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/api/v1/inboxes/"+in.ID+"/files", strings.NewReader("")); rec.Code != http.StatusBadRequest {
		t.Errorf("empty upload: status %d", rec.Code)
	}

	rec = e.manage("GET", "/files", in)
	var list manageInboxResponse
	json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != http.StatusOK || len(list.Files) != 1 || list.Uploads != 1 || list.Bytes != 14 || !list.Open {
		t.Fatalf("list: %d %+v", rec.Code, list)
	}
	f := list.Files[0]
	if !f.ExpiresAt.Equal(e.clock.Add(7 * 24 * time.Hour)) {
		t.Errorf("file expires %v", f.ExpiresAt)
	}

	// Only through the management token: not by ID, not via the public route.
	if rec := e.do("GET", "/api/v1/files/"+f.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("public download: status %d", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/inboxes/"+in.ID+"/files/"+f.ID, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("without token: status %d", rec.Code)
	}
	for range 2 {
		rec = e.manage("GET", "/files/"+f.ID, in)
		if rec.Code != http.StatusOK || rec.Body.String() != "ciphertext one" || rec.Header().Get("Content-Disposition") != "attachment" {
			t.Fatalf("download: %d %q", rec.Code, rec.Body)
		}
	}

	if rec := e.manage("DELETE", "/files/"+f.ID, in); rec.Code != http.StatusNoContent {
		t.Errorf("delete file: status %d", rec.Code)
	}
	if rec := e.manage("GET", "/files/"+f.ID, in); rec.Code != http.StatusNotFound {
		t.Errorf("after delete: status %d", rec.Code)
	}

	e.do("POST", "/api/v1/inboxes/"+in.ID+"/files", strings.NewReader("two"))
	if rec := e.manage("DELETE", "", in); rec.Code != http.StatusNoContent {
		t.Errorf("revoke: status %d", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/inboxes/"+in.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("info after revoke: status %d", rec.Code)
	}
	if rec := e.do("POST", "/api/v1/inboxes/"+in.ID+"/files", strings.NewReader("late")); rec.Code != http.StatusNotFound {
		t.Errorf("upload after revoke: status %d", rec.Code)
	}
	if rec := e.manage("GET", "/files", in); rec.Code != http.StatusNotFound {
		t.Errorf("manage after revoke: status %d", rec.Code)
	}
}

func TestInboxManageAuth(t *testing.T) {
	e := newTestEnv(t)
	a := e.createInbox(t, "")
	b := e.createInbox(t, "")
	for _, path := range []string{"/files", "/files/AAAAAAAAAAAAAAAAAAAAAA", ""} {
		method := "GET"
		if path == "" {
			method = "DELETE"
		}
		if rec := e.do(method, "/api/v1/inboxes/"+a.ID+path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %q without token: %d", method, path, rec.Code)
		}
		if rec := e.do(method, "/api/v1/inboxes/"+a.ID+path, nil, "Authorization", "Bearer "+b.ManageToken); rec.Code != http.StatusForbidden {
			t.Errorf("%s %q with another link's token: %d", method, path, rec.Code)
		}
	}
	if rec := e.manage("GET", "/files", a); rec.Code != http.StatusOK {
		t.Errorf("own token: %d", rec.Code)
	}
}

func TestInboxLimits(t *testing.T) {
	e := newTestEnv(t)
	in := e.createInbox(t, "?max_uploads=1")
	e.do("POST", "/api/v1/inboxes/"+in.ID+"/files", strings.NewReader("x"))
	if rec := e.do("POST", "/api/v1/inboxes/"+in.ID+"/files", strings.NewReader("y")); rec.Code != http.StatusConflict {
		t.Errorf("over the upload limit: status %d", rec.Code)
	}
	if rec := e.do("POST", "/api/v1/uploads?inbox="+in.ID, nil); rec.Code != http.StatusConflict {
		t.Errorf("chunked upload to a full link: status %d", rec.Code)
	}

	e.srv.cfg.InboxMaxSize = 10
	small := e.createInbox(t, "")
	if rec := e.do("POST", "/api/v1/inboxes/"+small.ID+"/files", strings.NewReader(strings.Repeat("a", 11))); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("over the byte limit: status %d", rec.Code)
	}
	e.do("POST", "/api/v1/inboxes/"+small.ID+"/files", strings.NewReader("123456"))
	var info inboxInfoResponse
	json.Unmarshal(e.do("GET", "/api/v1/inboxes/"+small.ID, nil).Body.Bytes(), &info)
	if info.BytesLeft != 4 || info.MaxFileSize != 4 {
		t.Errorf("info after 6 of 10 bytes: %+v", info)
	}

	e.srv.cfg.MaxInboxes = 2 // "in" and "small" are open
	rec := e.do("POST", "/api/v1/inboxes", strings.NewReader(testPublicKey))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("over the link cap: status %d", rec.Code)
	}
}

func TestInboxChunkedUpload(t *testing.T) {
	e := newTestEnv(t)
	e.srv.cfg.InboxMaxSize = 20
	in := e.createInbox(t, "")

	up := e.createUpload(t, "?inbox="+in.ID)
	if up.MaxSize != 20 {
		t.Errorf("max_size %d, want the link's 20", up.MaxSize)
	}
	if rec := e.chunk(up, 0, "chunk-one/"); rec.Code != http.StatusOK {
		t.Fatalf("chunk 1: %d %s", rec.Code, rec.Body)
	}
	if rec := e.chunk(up, 10, strings.Repeat("x", 11)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("chunk over the link's limit: %d", rec.Code)
	}
	e.chunk(up, 10, "chunk-two")
	rec := e.complete(up)
	if rec.Code != http.StatusCreated || strings.TrimSpace(rec.Body.String()) != `{"size":19}` {
		t.Fatalf("complete: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "delete_token") {
		t.Error("uploader got a delete token")
	}
	var list manageInboxResponse
	json.Unmarshal(e.manage("GET", "/files", in).Body.Bytes(), &list)
	if len(list.Files) != 1 || list.Files[0].Size != 19 {
		t.Fatalf("list %+v", list)
	}
	if body := e.manage("GET", "/files/"+list.Files[0].ID, in).Body.String(); body != "chunk-one/chunk-two" {
		t.Errorf("body %q", body)
	}
	if e.srv.reserved != 0 {
		t.Errorf("quota still reserved: %d", e.srv.reserved)
	}
	if rec := e.do("POST", "/api/v1/uploads?inbox=unknown", nil); rec.Code != http.StatusNotFound {
		t.Errorf("chunked upload to unknown link: %d", rec.Code)
	}
}
