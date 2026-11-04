package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/config"
	"github.com/FileBunny/filebunny/backend/internal/mail/mailtest"
	"github.com/FileBunny/filebunny/backend/internal/pow"
	"github.com/FileBunny/filebunny/backend/internal/ratelimit"
)

const clientHeader = "X-Test-Client"

// abuseEnv has proof of work (low difficulty) and limits on; visitors are
// told apart by clientHeader, as Caddy's header in production.
func abuseEnv(t *testing.T, bits int, limits bool) *testEnv {
	return newTestEnvWith(t, func(c *config.Config) {
		c.PowBits, c.RateLimits, c.ClientIPHeader = bits, limits, clientHeader
	})
}

// proof gets and solves a challenge for action as visitor ip.
func (e *testEnv) proof(t *testing.T, action, ip string) string {
	t.Helper()
	rec := e.do("GET", "/api/v1/challenge?action="+action, nil, clientHeader, ip)
	var c challengeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("challenge: %d %s", rec.Code, rec.Body)
	}
	return pow.Solve(pow.Challenge{Token: c.Challenge, Bits: c.Bits})
}

func TestProofOfWork(t *testing.T) {
	e := abuseEnv(t, 8, false)
	const ip = "203.0.113.7"
	post := func(target, body, proof string) int {
		return e.do("POST", target, strings.NewReader(body), clientHeader, ip, powHeader, proof).Code
	}

	if code := post("/api/v1/files", "x", ""); code != http.StatusForbidden {
		t.Errorf("upload without proof: %d", code)
	}
	p := e.proof(t, "upload", ip)
	if code := post("/api/v1/files", "x", p); code != http.StatusCreated {
		t.Fatalf("upload with proof: %d", code)
	}
	if code := post("/api/v1/files", "x", p); code != http.StatusForbidden {
		t.Errorf("proof used twice: %d", code)
	}
	if code := post("/api/v1/files", "x", e.proof(t, "mail", ip)); code != http.StatusForbidden {
		t.Errorf("proof for another action: %d", code)
	}
	if code := post("/api/v1/files", "x", e.proof(t, "upload", "198.51.100.1")); code != http.StatusForbidden {
		t.Errorf("proof of another visitor: %d", code)
	}

	// Every expensive request needs one.
	if code := post("/api/v1/uploads", "", ""); code != http.StatusForbidden {
		t.Errorf("chunked upload without proof: %d", code)
	}
	if code := post("/api/v1/uploads", "", e.proof(t, "upload", ip)); code != http.StatusCreated {
		t.Errorf("chunked upload with proof: %d", code)
	}
	if code := post("/api/v1/inboxes", testPublicKey, ""); code != http.StatusForbidden {
		t.Errorf("upload link without proof: %d", code)
	}
	rec := e.do("POST", "/api/v1/inboxes", strings.NewReader(testPublicKey), clientHeader, ip, powHeader, e.proof(t, "inbox", ip))
	var in createInboxResponse
	json.Unmarshal(rec.Body.Bytes(), &in)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload link with proof: %d", rec.Code)
	}
	if code := post("/api/v1/inboxes/"+in.ID+"/files", "x", ""); code != http.StatusForbidden {
		t.Errorf("upload to a link without proof: %d", code)
	}
	if code := post("/api/v1/inboxes/"+in.ID+"/files", "x", e.proof(t, "upload", ip)); code != http.StatusCreated {
		t.Errorf("upload to a link with proof: %d", code)
	}
	if code := post("/api/v1/uploads?inbox="+in.ID, "", ""); code != http.StatusForbidden {
		t.Errorf("chunked upload to a link without proof: %d", code)
	}

	relay := mailtest.Start(t, "one", false)
	withMail(t, e, relay)
	up := e.do("POST", "/api/v1/files", strings.NewReader("x"), clientHeader, ip, powHeader, e.proof(t, "upload", ip))
	var file uploadResponse
	json.Unmarshal(up.Body.Bytes(), &file)
	notify := func(proof string) int {
		return e.do("POST", "/api/v1/files/"+file.ID+"/notify", strings.NewReader(`{"email":"a@example.org"}`),
			"Authorization", "Bearer "+file.DeleteToken, clientHeader, ip, powHeader, proof).Code
	}
	if code := notify(""); code != http.StatusForbidden {
		t.Errorf("mail without proof: %d", code)
	}
	if code := notify(e.proof(t, "mail", ip)); code != http.StatusNoContent {
		t.Errorf("mail with proof: %d", code)
	}

	// Downloads need none.
	if code := e.do("GET", "/api/v1/files/"+file.ID, nil, clientHeader, ip).Code; code != http.StatusOK {
		t.Errorf("download: %d", code)
	}
}

func TestChallenge(t *testing.T) {
	e := abuseEnv(t, 10, true)
	const ip = "203.0.113.7"
	challenge := func() challengeResponse {
		var c challengeResponse
		json.Unmarshal(e.do("GET", "/api/v1/challenge?action=upload", nil, clientHeader, ip).Body.Bytes(), &c)
		return c
	}
	if c := challenge(); c.Bits != 10 || c.Challenge == "" || !c.ExpiresAt.Equal(e.clock.Add(pow.Lifetime)) {
		t.Fatalf("challenge %+v", c)
	}
	// The difficulty rises with what the visitor did this hour.
	for range 4 {
		e.do("POST", "/api/v1/files", strings.NewReader("x"), clientHeader, ip, powHeader, e.proof(t, "upload", ip))
	}
	if c := challenge(); c.Bits != 11 {
		t.Errorf("after 4 uploads: %d bits, want 11", c.Bits)
	}
	for range 4 {
		e.do("POST", "/api/v1/files", strings.NewReader("x"), clientHeader, ip, powHeader, e.proof(t, "upload", ip))
	}
	if c := challenge(); c.Bits != 12 {
		t.Errorf("after 8 uploads: %d bits, want 12", c.Bits)
	}
	if rec := e.do("GET", "/api/v1/challenge?action=delete", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown action: %d", rec.Code)
	}

	off := newTestEnv(t)
	if body := strings.TrimSpace(off.do("GET", "/api/v1/challenge?action=upload", nil).Body.String()); body != `{"challenge":"","bits":0}` {
		t.Errorf("proof of work off: %s", body)
	}
}

func TestRateLimits(t *testing.T) {
	e := abuseEnv(t, 0, true)
	e.srv.limiter = ratelimit.New(map[ratelimit.Action]ratelimit.Limit{
		ratelimit.Upload:      {PerHour: 2},
		ratelimit.UploadBytes: {PerHour: 10},
		ratelimit.Inbox:       {PerHour: 1},
		ratelimit.WKD:         {PerHour: 1},
		ratelimit.Download:    {PerHour: 1},
	}, func() time.Time { return e.clock })
	const a, b = "203.0.113.7", "2001:db8:1:2::7"
	upload := func(ip, body string) *httpResult {
		rec := e.do("POST", "/api/v1/files", strings.NewReader(body), clientHeader, ip)
		return &httpResult{rec.Code, rec.Header().Get("Retry-After")}
	}

	if r := upload(a, "123456789012"); r.code != http.StatusCreated {
		t.Fatalf("first upload: %d", r.code)
	}
	// 12 bytes used of 10: no room for more, although one upload is left.
	if r := upload(a, "x"); r.code != http.StatusTooManyRequests || r.retry == "" {
		t.Errorf("over the byte limit: %+v", r)
	}
	if r := upload(b, "x"); r.code != http.StatusCreated {
		t.Errorf("another visitor: %d", r.code)
	}
	if r := upload("2001:db8:1:2::8", "x"); r.code != http.StatusCreated {
		t.Errorf("same /64, second upload: %d", r.code)
	}
	if r := upload("2001:db8:1:2::9", "x"); r.code != http.StatusTooManyRequests {
		t.Errorf("same /64, third upload: %d", r.code)
	}
	if code := e.do("POST", "/api/v1/uploads", nil, clientHeader, b).Code; code != http.StatusTooManyRequests {
		t.Errorf("chunked upload over the limit: %d", code)
	}

	if code := e.do("POST", "/api/v1/inboxes", strings.NewReader(testPublicKey), clientHeader, a).Code; code != http.StatusCreated {
		t.Errorf("first upload link: %d", code)
	}
	if code := e.do("POST", "/api/v1/inboxes", strings.NewReader(testPublicKey), clientHeader, a).Code; code != http.StatusTooManyRequests {
		t.Errorf("second upload link: %d", code)
	}
	for i, want := range []int{http.StatusNotFound, http.StatusTooManyRequests} {
		if code := e.do("POST", "/api/v1/wkd", wkdBody("x@example.org"), clientHeader, a).Code; code != want {
			t.Errorf("WKD lookup %d: %d, want %d", i+1, code, want)
		}
	}
	for i, want := range []int{http.StatusNotFound, http.StatusTooManyRequests} {
		if code := e.do("GET", "/api/v1/files/AAAAAAAAAAAAAAAAAAAAAA", nil, clientHeader, a).Code; code != want {
			t.Errorf("download %d: %d, want %d", i+1, code, want)
		}
	}

	// The next hour starts over.
	e.clock = e.clock.Add(time.Hour)
	if r := upload("2001:db8:1:2::9", "x"); r.code != http.StatusCreated {
		t.Errorf("next hour: %d", r.code)
	}
	for _, addr := range []string{"203.0.113", "2001:db8"} {
		if strings.Contains(e.logs.String(), addr) {
			t.Errorf("%s in logs:\n%s", addr, e.logs)
		}
	}
}

type httpResult struct {
	code  int
	retry string
}

// Chunks count their bytes as they arrive.
func TestRateLimitChunkBytes(t *testing.T) {
	e := abuseEnv(t, 0, true)
	e.srv.limiter = ratelimit.New(map[ratelimit.Action]ratelimit.Limit{ratelimit.UploadBytes: {PerHour: 10}},
		func() time.Time { return e.clock })
	rec := e.do("POST", "/api/v1/uploads", nil)
	var up createUploadResponse
	json.Unmarshal(rec.Body.Bytes(), &up)
	if rec := e.chunk(up, 0, "123456789012"); rec.Code != http.StatusOK {
		t.Fatalf("chunk 1: %d", rec.Code)
	}
	if rec := e.chunk(up, 12, "x"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("chunk over the byte limit: %d", rec.Code)
	}
}

// Without a configured header, the connection's address counts, and a
// header a client sends means nothing.
func TestClientHeaderOnlyWhenConfigured(t *testing.T) {
	e := newTestEnvWith(t, func(c *config.Config) { c.PowBits, c.RateLimits = 0, true })
	e.srv.limiter = ratelimit.New(map[ratelimit.Action]ratelimit.Limit{ratelimit.Upload: {PerHour: 1}},
		func() time.Time { return e.clock })
	e.do("POST", "/api/v1/files", strings.NewReader("x"), "X-FileBunny-Client", "203.0.113.1")
	if code := e.do("POST", "/api/v1/files", strings.NewReader("x"), "X-FileBunny-Client", "203.0.113.2").Code; code != http.StatusTooManyRequests {
		t.Errorf("a client-sent header changed the visitor: %d", code)
	}
	if strings.Contains(strconv.Quote(e.logs.String()), "203.0.113") {
		t.Error("address in logs")
	}
}
