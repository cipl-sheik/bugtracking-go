package bugtrack

import (
	"crypto/rand"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	sdkName = "bugtrack-go"

	maxExceptionMessage = 2000
	maxStackFrames      = 128
	maxTagEntries       = 64
	maxTagKeyLen        = 128
	maxTagValueLen      = 200
	maxHeaders          = 32
	maxHeaderValueLen   = 256
	maxRetryAfter       = 2 * time.Second
)

// Version is the SDK version sent as sdk.version.
const Version = "0.1.0"

// Result is the 202 Accepted ingest body.
// EventID is the stored event row id. EventUUID is the client-generated id.
type Result struct {
	EventID   uint64 `json:"event_id"`
	IssueID   uint64 `json:"issue_id"`
	EventUUID string `json:"event_uuid"`
}

// APIError is a non-2xx response from the ingest API.
type APIError struct {
	Status     int
	Code       string
	Message    string
	RequestID  string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e == nil {
		return "bugtrack: api error"
	}
	if e.Code != "" {
		return fmt.Sprintf("bugtrack: %s: %s", e.Code, e.Message)
	}
	if e.Message != "" {
		return fmt.Sprintf("bugtrack: http %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("bugtrack: http %d", e.Status)
}

type event struct {
	EventID     string            `json:"event_id"`
	Level       string            `json:"level"`
	Environment string            `json:"environment,omitempty"`
	Platform    string            `json:"platform"`
	Release     string            `json:"release,omitempty"`
	Timestamp   string            `json:"timestamp"`
	Transaction string            `json:"transaction,omitempty"`
	ServerName  string            `json:"server_name,omitempty"`
	Exception   exceptionBody     `json:"exception"`
	Stacktrace  []frame           `json:"stacktrace,omitempty"`
	Request     map[string]any    `json:"request,omitempty"`
	User        map[string]any    `json:"user,omitempty"`
	Runtime     map[string]string `json:"runtime,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Extra       map[string]any    `json:"extra,omitempty"`
	SDK         sdkBody           `json:"sdk"`
}

type exceptionBody struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	File     string `json:"file,omitempty"`
	Line     *uint  `json:"line,omitempty"`
	Function string `json:"function,omitempty"`
}

type frame struct {
	Filename   string `json:"filename,omitempty"`
	Function   string `json:"function,omitempty"`
	LineNumber int    `json:"lineno,omitempty"`
	AbsPath    string `json:"abs_path,omitempty"`
	InApp      bool   `json:"in_app"`
	Module     string `json:"module,omitempty"`
}

type sdkBody struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type eventMeta struct {
	level       string
	transaction string
	tags        map[string]string
	extra       map[string]any
	user        map[string]any
	request     map[string]any
}

func newEventID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("bugtrack: event id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func exceptionType(err error) string {
	t := strings.TrimPrefix(fmt.Sprintf("%T", err), "*")
	if t == "" || t == "errors.errorString" {
		return "error"
	}
	return t
}

func describe(rec any) (typ, msg string) {
	switch v := rec.(type) {
	case error:
		return exceptionType(v), v.Error()
	case string:
		return "panic", v
	default:
		return "panic", fmt.Sprint(v)
	}
}

func truncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func boundTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	cleaned := make(map[string]string, len(in))
	for k, v := range in {
		k = truncateBytes(strings.TrimSpace(k), maxTagKeyLen)
		if k == "" {
			continue
		}
		cleaned[k] = truncateBytes(v, maxTagValueLen)
	}
	if len(cleaned) == 0 {
		return nil
	}
	keys := make([]string, 0, len(cleaned))
	for k := range cleaned {
		keys = append(keys, k)
	}
	if len(keys) > maxTagEntries {
		sort.Strings(keys)
		keys = keys[:maxTagEntries]
		trimmed := make(map[string]string, len(keys))
		for _, k := range keys {
			trimmed[k] = cleaned[k]
		}
		return trimmed
	}
	return cleaned
}
