package bugtrack_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cipl-sheik/bugtracking-go"
)

var eventIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type recorded struct {
	mu       sync.Mutex
	header   string
	bodies   []map[string]any
	statuses []int
}

func (r *recorded) handler(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "json", http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	r.header = req.Header.Get("X-BugTrack-Key")
	r.bodies = append(r.bodies, payload)
	status := http.StatusAccepted
	if len(r.statuses) > 0 {
		status = r.statuses[0]
		r.statuses = r.statuses[1:]
	}
	r.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "0")
	}
	w.WriteHeader(status)
	if status >= 200 && status < 300 {
		eventUUID, _ := payload["event_id"].(string)
		_, _ = w.Write([]byte(`{"event_id":45,"issue_id":9,"event_uuid":"` + eventUUID + `"}`))
		return
	}
	_, _ = fmt.Fprintf(w, `{"error":{"code":"failed","message":"status %d","request_id":"req-1"}}`, status)
}

func (r *recorded) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func (r *recorded) body(i int) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bodies[i]
}

func testClient(t *testing.T, srv *httptest.Server, extra bugtrack.Options) *bugtrack.Client {
	t.Helper()
	opts := bugtrack.Options{
		Endpoint:    srv.URL,
		APIKey:      "btk_ab_cd",
		Environment: "staging",
		Release:     "1.2.3",
		ServerName:  "api-1",
		Timeout:     time.Second,
	}
	if extra.HTTPClient != nil {
		opts.HTTPClient = extra.HTTPClient
	}
	if extra.ReportErrors != nil {
		opts.ReportErrors = extra.ReportErrors
	}
	if extra.DSN != "" {
		opts.DSN = extra.DSN
		opts.Endpoint = ""
		opts.APIKey = ""
	}
	client, err := bugtrack.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func TestCapturePostsIngestContract(t *testing.T) {
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	t.Cleanup(srv.Close)
	client := testClient(t, srv, bugtrack.Options{})

	result, err := client.Capture(context.Background(), errors.New("disk full"),
		bugtrack.WithTag("queue", "billing"),
		bugtrack.WithUser(map[string]any{"id": "user-7"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.EventID != 45 || result.IssueID != 9 {
		t.Fatalf("result = %+v", result)
	}
	if rec.header != "btk_ab_cd" {
		t.Fatalf("api key header = %q", rec.header)
	}
	if rec.calls() != 1 {
		t.Fatalf("calls = %d", rec.calls())
	}
	body := rec.body(0)
	if body["platform"] != "go" || body["level"] != "error" || body["environment"] != "staging" || body["release"] != "1.2.3" || body["server_name"] != "api-1" {
		t.Fatalf("event = %#v", body)
	}
	eventID, _ := body["event_id"].(string)
	if !eventIDPattern.MatchString(eventID) || result.EventUUID != eventID {
		t.Fatalf("event id = %q result uuid = %q", eventID, result.EventUUID)
	}
	sdk, _ := body["sdk"].(map[string]any)
	if sdk["name"] != "bugtrack-go" || sdk["version"] != bugtrack.Version {
		t.Fatalf("sdk = %#v", sdk)
	}
	ex, _ := body["exception"].(map[string]any)
	if ex["type"] != "error" || ex["message"] != "disk full" {
		t.Fatalf("exception = %#v", ex)
	}
	if !strings.Contains(fmt.Sprint(ex["function"]), "reportSample") && !strings.Contains(fmt.Sprint(ex["function"]), "TestCapturePostsIngestContract") {
		t.Fatalf("exception function = %#v", ex["function"])
	}
	frames, _ := body["stacktrace"].([]any)
	if len(frames) == 0 {
		t.Fatal("expected stack frames")
	}
	for _, raw := range frames {
		frame, _ := raw.(map[string]any)
		fn, _ := frame["function"].(string)
		if strings.HasPrefix(fn, "github.com/cipl-sheik/bugtracking-go.") {
			t.Fatalf("sdk frame leaked: %s", fn)
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "btk_ab_cd") {
		t.Fatal("api key leaked into the event body")
	}
}

func reportSample(client *bugtrack.Client) (*bugtrack.Result, error) {
	return client.Capture(context.Background(), errors.New("disk full"), bugtrack.WithTag("queue", "billing"))
}

func TestCaptureStackStartsAtCaller(t *testing.T) {
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	t.Cleanup(srv.Close)
	client := testClient(t, srv, bugtrack.Options{})

	if _, err := reportSample(client); err != nil {
		t.Fatal(err)
	}
	ex, _ := rec.body(0)["exception"].(map[string]any)
	if !strings.Contains(fmt.Sprint(ex["function"]), "reportSample") {
		t.Fatalf("top function = %#v", ex["function"])
	}
}

func TestCaptureNilDoesNotSend(t *testing.T) {
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	t.Cleanup(srv.Close)
	client := testClient(t, srv, bugtrack.Options{})
	result, err := client.Capture(context.Background(), nil)
	if err != nil || result != nil {
		t.Fatalf("result=%v err=%v", result, err)
	}
	if rec.calls() != 0 {
		t.Fatalf("calls = %d", rec.calls())
	}
}

func TestCaptureMessageAndTruncation(t *testing.T) {
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	t.Cleanup(srv.Close)
	client := testClient(t, srv, bugtrack.Options{})

	if _, err := client.CaptureMessage(context.Background(), "   "); err == nil {
		t.Fatal("expected empty message error")
	}
	if rec.calls() != 0 {
		t.Fatal("empty message called the API")
	}
	message := strings.Repeat("a", 1998) + "€"
	if _, err := client.CaptureMessage(context.Background(), message, bugtrack.WithLevel("warning")); err != nil {
		t.Fatal(err)
	}
	body := rec.body(0)
	if body["level"] != "warning" {
		t.Fatalf("level = %#v", body["level"])
	}
	ex, _ := body["exception"].(map[string]any)
	got, _ := ex["message"].(string)
	if strings.ContainsRune(got, '€') || len(got) != 1998 || !utf8Valid(got) {
		t.Fatalf("message len=%d", len(got))
	}
	if ex["type"] != "message" {
		t.Fatalf("type = %#v", ex["type"])
	}
}

func utf8Valid(s string) bool {
	return strings.ToValidUTF8(s, "") == s
}

func TestDeepStackIsCapped(t *testing.T) {
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	t.Cleanup(srv.Close)
	client := testClient(t, srv, bugtrack.Options{})
	if _, err := recurseCapture(client, 200); err != nil {
		t.Fatal(err)
	}
	frames, _ := rec.body(0)["stacktrace"].([]any)
	if len(frames) != 128 {
		t.Fatalf("frames = %d", len(frames))
	}
}

func recurseCapture(client *bugtrack.Client, n int) (*bugtrack.Result, error) {
	if n == 0 {
		return client.Capture(context.Background(), errors.New("deep"))
	}
	return recurseCapture(client, n-1)
}

func TestRetryPolicy(t *testing.T) {
	cases := []struct {
		name      string
		statuses  []int
		wantCalls int
		wantErr   bool
	}{
		{name: "unauthorized", statuses: []int{http.StatusUnauthorized}, wantCalls: 1, wantErr: true},
		{name: "validation", statuses: []int{http.StatusUnprocessableEntity}, wantCalls: 1, wantErr: true},
		{name: "conflict", statuses: []int{http.StatusConflict}, wantCalls: 1, wantErr: true},
		{name: "server then ok", statuses: []int{http.StatusInternalServerError, http.StatusAccepted}, wantCalls: 2, wantErr: false},
		{name: "rate limit then ok", statuses: []int{http.StatusTooManyRequests, http.StatusAccepted}, wantCalls: 2, wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorded{statuses: append([]int(nil), tc.statuses...)}
			srv := httptest.NewServer(http.HandlerFunc(rec.handler))
			t.Cleanup(srv.Close)
			var reported error
			client := testClient(t, srv, bugtrack.Options{ReportErrors: func(err error) { reported = err }})
			_, err := client.Capture(context.Background(), errors.New("boom"))
			if tc.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatal(err)
			}
			if rec.calls() != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", rec.calls(), tc.wantCalls)
			}
			if tc.wantCalls == 2 {
				first, _ := rec.body(0)["event_id"].(string)
				second, _ := rec.body(1)["event_id"].(string)
				if first == "" || first != second {
					t.Fatalf("event ids = %q %q", first, second)
				}
			}
			if tc.wantErr {
				var api *bugtrack.APIError
				if !errors.As(err, &api) || api.RequestID != "req-1" {
					t.Fatalf("err = %v", err)
				}
				if reported == nil {
					t.Fatal("expected ReportErrors")
				}
			} else if reported != nil {
				t.Fatalf("unexpected report: %v", reported)
			}
		})
	}
}

type flakyTransport struct {
	base http.RoundTripper
	fail int
	n    int
}

func (f *flakyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.n++
	if f.n <= f.fail {
		return nil, errors.New("connection reset")
	}
	return f.base.RoundTrip(r)
}

func TestNetworkErrorRetriesOnce(t *testing.T) {
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	t.Cleanup(srv.Close)
	transport := &flakyTransport{base: http.DefaultTransport, fail: 1}
	client := testClient(t, srv, bugtrack.Options{HTTPClient: &http.Client{Transport: transport}})
	if _, err := client.Capture(context.Background(), errors.New("net")); err != nil {
		t.Fatal(err)
	}
	if transport.n != 2 || rec.calls() != 1 {
		t.Fatalf("transport calls = %d ingest calls = %d", transport.n, rec.calls())
	}
}

func TestRecoverRepanics(t *testing.T) {
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	t.Cleanup(srv.Close)
	client := testClient(t, srv, bugtrack.Options{})

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected repanic")
		}
		if fmt.Sprint(recovered) != "explode" {
			t.Fatalf("recovered = %v", recovered)
		}
		if rec.calls() != 1 {
			t.Fatalf("calls = %d", rec.calls())
		}
		ex, _ := rec.body(0)["exception"].(map[string]any)
		if rec.body(0)["level"] != "fatal" || ex["message"] != "explode" {
			t.Fatalf("event = %#v", rec.body(0))
		}
	}()
	func() {
		defer client.Recover(context.Background())
		panic("explode")
	}()
	t.Fatal("panic was swallowed")
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := bugtrack.New(bugtrack.Options{}); err == nil {
		t.Fatal("expected error")
	}
	cases := []bugtrack.Options{
		{DSN: "http://prefixonly@127.0.0.1:8080/1"},
		{DSN: "http://btk_gg_ab@127.0.0.1:8080/1"},
		{DSN: "http://gg:ab@127.0.0.1:8080/1"},
		{Endpoint: "ftp://127.0.0.1:8080", APIKey: "btk_ab_cd"},
		{Endpoint: "http://127.0.0.1:8080/api", APIKey: "btk_ab_cd"},
		{Endpoint: "http://btk_ab_cd@127.0.0.1:8080", APIKey: "btk_ab_cd"},
		{APIKey: "btk_ab_cd"},
	}
	for _, opts := range cases {
		if _, err := bugtrack.New(opts); err == nil {
			t.Fatalf("expected error for %+v", opts)
		}
	}
}

func TestDSNShapes(t *testing.T) {
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	client := testClient(t, srv, bugtrack.Options{DSN: "http://AB:CD@" + host + "/42"})
	if _, err := client.CaptureMessage(context.Background(), "from userinfo"); err != nil {
		t.Fatal(err)
	}
	if rec.header != "btk_AB_CD" {
		t.Fatalf("header = %q", rec.header)
	}

	client = testClient(t, srv, bugtrack.Options{DSN: "http://btk_ab_cd@" + host + "/7"})
	if _, err := client.CaptureMessage(context.Background(), "from opaque key"); err != nil {
		t.Fatal(err)
	}
	if rec.header != "btk_ab_cd" {
		t.Fatalf("header = %q", rec.header)
	}
}
