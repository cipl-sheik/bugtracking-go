package bugtrack_test

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cipl-sheik/bugtracking-go"
)

func TestHTTPMiddlewareReportsPanicAndRepanics(t *testing.T) {
	var mu sync.Mutex
	var body map[string]any
	var calls int
	ingest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		mu.Lock()
		calls++
		body = payload
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"event_id":1,"issue_id":2,"event_uuid":"11111111-1111-4111-8111-111111111111"}`))
	}))
	t.Cleanup(ingest.Close)

	client, err := bugtrack.New(bugtrack.Options{
		Endpoint:   ingest.URL,
		APIKey:     "btk_ab_cd",
		ServerName: "web",
		Timeout:    time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	app := httptest.NewServer(client.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panic" {
			panic("handler failed")
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	t.Cleanup(app.Close)
	app.Config.ErrorLog = log.New(io.Discard, "", 0)

	okResp, err := http.Get(app.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	okResp.Body.Close()
	if okResp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", okResp.StatusCode)
	}
	mu.Lock()
	if calls != 0 {
		t.Fatalf("healthy request reported %d events", calls)
	}
	mu.Unlock()

	req, err := http.NewRequest(http.MethodPost, app.URL+"/panic?token=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer super-secret")
	req.Header.Set("Cookie", "session=secret-cookie")
	req.Header.Set("X-BugTrack-Key", "btk_11_22")
	req.Header.Set("X-Request-Id", "trace-1")
	panicResp, err := http.DefaultClient.Do(req)
	if err != nil {
		if !strings.Contains(err.Error(), "EOF") {
			t.Fatal(err)
		}
	} else {
		panicResp.Body.Close()
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	if body["level"] != "fatal" || body["transaction"] != "POST /panic" {
		t.Fatalf("event = %#v", body)
	}
	ex, _ := body["exception"].(map[string]any)
	if ex["type"] != "panic" || ex["message"] != "handler failed" {
		t.Fatalf("exception = %#v", ex)
	}
	encoded, _ := json.Marshal(body["request"])
	text := string(encoded)
	if strings.Contains(text, "super-secret") || strings.Contains(text, "secret-cookie") || strings.Contains(text, "btk_11_22") || strings.Contains(text, "token=secret") {
		t.Fatalf("request leaked secrets: %s", text)
	}
	if !strings.Contains(text, "trace-1") || !strings.Contains(text, "/panic") {
		t.Fatalf("request = %s", text)
	}
}
