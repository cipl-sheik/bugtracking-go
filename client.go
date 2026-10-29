package bugtrack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Options configures a Client. Set DSN, or set both Endpoint and APIKey.
type Options struct {
	// DSN is http(s)://btk_<prefix>_<secret>@host[:port]/<projectID>
	// or http(s)://<prefix>:<secret>@host[:port]/<projectID>.
	// When DSN is set, Endpoint and APIKey are ignored.
	DSN string

	// Endpoint is the API origin, for example http://127.0.0.1:8080.
	// APIKey is the project key btk_<prefix>_<secret>.
	Endpoint string
	APIKey   string

	// Environment defaults to production. Release is sent when set.
	Environment string
	Release     string

	// ServerName defaults to the machine hostname.
	ServerName string

	// Timeout bounds each ingest attempt. It defaults to 2 seconds.
	Timeout time.Duration

	// HTTPClient replaces the default client when set. Leave Timeout at 0
	// unless it is at least Options.Timeout. Redirects on a custom client can
	// forward the API key; the default client does not follow redirects.
	HTTPClient *http.Client

	// ReportErrors receives the final ingest failure when Capture returns an
	// error. Context cancellation is not reported. The hook must not panic.
	ReportErrors func(error)
}

// Client sends events to a BugTrack ingest API.
type Client struct {
	ingestURL   string
	apiKey      string
	environment string
	release     string
	serverName  string
	timeout     time.Duration
	http        *http.Client
	report      func(error)
}

// New validates options and returns a client. It does not contact the API.
func New(opts Options) (*Client, error) {
	endpoint, apiKey, err := resolveOptions(opts)
	if err != nil {
		return nil, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	env := strings.TrimSpace(opts.Environment)
	if env == "" {
		env = "production"
	}
	serverName := strings.TrimSpace(opts.ServerName)
	if serverName == "" {
		if host, hostErr := os.Hostname(); hostErr == nil {
			serverName = strings.TrimSpace(host)
		}
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Client{
		ingestURL:   endpoint + "/api/v1/ingest",
		apiKey:      apiKey,
		environment: env,
		release:     strings.TrimSpace(opts.Release),
		serverName:  serverName,
		timeout:     timeout,
		http:        hc,
		report:      opts.ReportErrors,
	}, nil
}

// EventOption adds context to a single event.
type EventOption func(*eventMeta)

// WithLevel overrides the default level (error, fatal, or info).
func WithLevel(level string) EventOption {
	return func(m *eventMeta) {
		m.level = strings.TrimSpace(level)
	}
}

// WithTransaction sets the transaction name, usually "GET /orders/:id".
func WithTransaction(name string) EventOption {
	return func(m *eventMeta) {
		m.transaction = strings.TrimSpace(name)
	}
}

// WithTag adds one tag. Empty keys are dropped. At most 64 tags are sent.
func WithTag(key, value string) EventOption {
	return func(m *eventMeta) {
		if m.tags == nil {
			m.tags = make(map[string]string)
		}
		m.tags[key] = value
	}
}

// WithTags copies tags onto the event.
func WithTags(tags map[string]string) EventOption {
	return func(m *eventMeta) {
		if len(tags) == 0 {
			return
		}
		if m.tags == nil {
			m.tags = make(map[string]string, len(tags))
		}
		for k, v := range tags {
			m.tags[k] = v
		}
	}
}

// WithExtra attaches caller-defined context. Do not put secrets here.
func WithExtra(extra map[string]any) EventOption {
	return func(m *eventMeta) {
		if len(extra) == 0 {
			return
		}
		if m.extra == nil {
			m.extra = make(map[string]any, len(extra))
		}
		for k, v := range extra {
			m.extra[k] = v
		}
	}
}

// WithUser attaches a user map, for example id and email.
func WithUser(user map[string]any) EventOption {
	return func(m *eventMeta) {
		if len(user) == 0 {
			return
		}
		if m.user == nil {
			m.user = make(map[string]any, len(user))
		}
		for k, v := range user {
			m.user[k] = v
		}
	}
}

// WithRequest attaches a request map. Prefer RequestFields, which strips secrets.
func WithRequest(req map[string]any) EventOption {
	return func(m *eventMeta) {
		m.request = req
	}
}

// Capture reports err. A nil error is ignored and does not call the API.
// The default level is error.
func (c *Client) Capture(ctx context.Context, err error, opts ...EventOption) (*Result, error) {
	if err == nil {
		return nil, nil
	}
	if c == nil {
		return nil, errors.New("bugtrack: client is nil")
	}
	frames := captureFrames()
	return c.deliver(ctx, exceptionType(err), err.Error(), "error", frames, opts)
}

// CaptureMessage reports a message that is not an error value.
// The default level is info. An empty message returns an error and does not call the API.
func (c *Client) CaptureMessage(ctx context.Context, message string, opts ...EventOption) (*Result, error) {
	if c == nil {
		return nil, errors.New("bugtrack: client is nil")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, errors.New("bugtrack: message is empty")
	}
	frames := captureFrames()
	return c.deliver(ctx, "message", message, "info", frames, opts)
}

// CapturePanic reports a value received from recover. A nil value is ignored.
// The default level is fatal. Unlike Recover, CapturePanic does not re-panic.
func (c *Client) CapturePanic(ctx context.Context, recovered any, opts ...EventOption) (*Result, error) {
	if recovered == nil {
		return nil, nil
	}
	if c == nil {
		return nil, errors.New("bugtrack: client is nil")
	}
	frames := captureFrames()
	typ, msg := describe(recovered)
	return c.deliver(ctx, typ, msg, "fatal", frames, opts)
}

// Recover reports the current panic and then re-panics with the same value.
// Use it as defer client.Recover(ctx). A nil client still re-panics.
func (c *Client) Recover(ctx context.Context, opts ...EventOption) {
	recovered := recover()
	if recovered == nil {
		return
	}
	if c != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		_, _ = c.CapturePanic(ctx, recovered, opts...)
	}
	panic(recovered)
}

func (c *Client) deliver(ctx context.Context, typ, msg, level string, frames []frame, opts []EventOption) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ev, err := c.buildEvent(typ, msg, level, frames, opts)
	if err != nil {
		c.reportErr(err)
		return nil, err
	}
	body, err := json.Marshal(ev)
	if err != nil {
		err = fmt.Errorf("bugtrack: encode event: %w", err)
		c.reportErr(err)
		return nil, err
	}
	res, err := c.post(ctx, body)
	if err == nil {
		return res, nil
	}
	if ctx.Err() != nil || !shouldRetry(err) {
		c.reportErr(err)
		return nil, err
	}
	if wait := retryWait(err); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			c.reportErr(ctx.Err())
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	res, err = c.post(ctx, body)
	if err != nil {
		c.reportErr(err)
	}
	return res, err
}

func (c *Client) buildEvent(typ, msg, level string, frames []frame, opts []EventOption) (*event, error) {
	meta := eventMeta{level: level}
	for _, opt := range opts {
		if opt != nil {
			opt(&meta)
		}
	}
	if strings.TrimSpace(meta.level) == "" {
		meta.level = level
	}
	id, err := newEventID()
	if err != nil {
		return nil, err
	}
	ex := exceptionBody{
		Type:    truncateBytes(typ, 512),
		Message: truncateBytes(msg, maxExceptionMessage),
	}
	applyTopFrame(&ex, frames)
	return &event{
		EventID:     id,
		Level:       meta.level,
		Environment: c.environment,
		Platform:    "go",
		Release:     c.release,
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		Transaction: truncateBytes(meta.transaction, 255),
		ServerName:  c.serverName,
		Exception:   ex,
		Stacktrace:  frames,
		Request:     meta.request,
		User:        meta.user,
		Runtime: map[string]string{
			"name":    "go",
			"version": runtime.Version(),
			"os":      runtime.GOOS,
			"arch":    runtime.GOARCH,
		},
		Tags:  boundTags(meta.tags),
		Extra: meta.extra,
		SDK:   sdkBody{Name: sdkName, Version: Version},
	}, nil
}

func (c *Client) post(ctx context.Context, body []byte) (*Result, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ingestURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bugtrack: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", sdkName+"/"+Version)
	// Header.Set canonicalizes to X-Bugtrack-Key. Assign the preferred name directly.
	req.Header["X-BugTrack-Key"] = []string{c.apiKey}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("bugtrack: read response: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var result Result
		if err := json.Unmarshal(payload, &result); err != nil {
			return nil, fmt.Errorf("bugtrack: decode ingest response: %w", err)
		}
		return &result, nil
	}
	return nil, apiError(resp.StatusCode, payload, parseRetryAfter(resp.Header.Get("Retry-After")))
}

func (c *Client) reportErr(err error) {
	if err == nil || c.report == nil || errors.Is(err, context.Canceled) {
		return
	}
	c.report(err)
}

func shouldRetry(err error) bool {
	var api *APIError
	if errors.As(err, &api) {
		return api.Status == http.StatusTooManyRequests || api.Status >= 500
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return !errors.Is(urlErr.Err, context.Canceled) && !errors.Is(urlErr.Err, context.DeadlineExceeded)
	}
	return false
}

func retryWait(err error) time.Duration {
	var api *APIError
	if !errors.As(err, &api) || api.RetryAfter <= 0 {
		return 0
	}
	if api.RetryAfter > maxRetryAfter {
		return maxRetryAfter
	}
	return api.RetryAfter
}

func apiError(status int, body []byte, retryAfter time.Duration) *APIError {
	api := &APIError{Status: status, RetryAfter: retryAfter}
	var env struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil && (env.Error.Code != "" || env.Error.Message != "") {
		api.Code = env.Error.Code
		api.Message = env.Error.Message
		api.RequestID = env.Error.RequestID
		return api
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 512 {
		msg = msg[:512]
	}
	api.Message = msg
	return api
}

func parseRetryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if sec, err := strconv.Atoi(h); err == nil {
		if sec <= 0 {
			return 0
		}
		return time.Duration(sec) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}
