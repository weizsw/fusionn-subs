package callback

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/fusionn-subs/pkg/logger"
)

func newLogSink(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	previous := logger.Log
	logger.Log = zap.New(core).Sugar()
	t.Cleanup(func() { logger.Log = previous })
	return logs
}

func entriesForEvent(logs *observer.ObservedLogs, event string) []observer.LoggedEntry {
	var entries []observer.LoggedEntry
	for _, entry := range logs.All() {
		if entry.ContextMap()["event"] == event {
			entries = append(entries, entry)
		}
	}
	return entries
}

func countErrors(logs *observer.ObservedLogs) int {
	count := 0
	for _, entry := range logs.All() {
		if entry.Level == zapcore.ErrorLevel {
			count++
		}
	}
	return count
}

func TestSendLogsRetryAndLeavesFinalFailureToCaller(t *testing.T) {
	const bodyMarker = "response body must not reach the log"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(bodyMarker))
	}))
	defer server.Close()

	logs := newLogSink(t)
	client := NewClient(server.URL, time.Second, 1, []int{0})

	err := client.Send(context.Background(), Payload{JobID: "job-1"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("error = %q, want the status", err.Error())
	}
	if strings.Contains(err.Error(), bodyMarker) {
		t.Fatalf("error embeds the response body: %q", err.Error())
	}

	retries := entriesForEvent(logs, "callback_retry")
	if len(retries) != 1 {
		t.Fatalf("callback_retry entries = %d, want 1", len(retries))
	}
	if retries[0].Level != zapcore.WarnLevel {
		t.Fatalf("callback_retry level = %v, want WARN", retries[0].Level)
	}
	for _, entry := range logs.All() {
		if value, ok := entry.ContextMap()["error"].(string); ok && strings.Contains(value, bodyMarker) {
			t.Fatal("a log entry embeds the response body")
		}
	}
	if got := countErrors(logs); got != 0 {
		t.Fatalf("ERROR entries = %d, want 0 (the worker boundary owns the failure)", got)
	}
}

func TestSendLogsDeliveredOnce(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logs := newLogSink(t)
	client := NewClient(server.URL, time.Second, 0, []int{0})

	if err := client.Send(context.Background(), Payload{JobID: "job-1"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	delivered := entriesForEvent(logs, "callback_delivered")
	if len(delivered) != 1 {
		t.Fatalf("callback_delivered entries = %d, want 1", len(delivered))
	}
	deliveredContext := delivered[0].ContextMap()
	if deliveredContext["job_id"] != "job-1" {
		t.Fatalf("job_id = %v, want job-1", deliveredContext["job_id"])
	}
	if deliveredContext["status_code"] != int64(http.StatusOK) {
		t.Fatalf("status_code = %v, want 200", deliveredContext["status_code"])
	}
	if got := countErrors(logs); got != 0 {
		t.Fatalf("ERROR entries = %d, want 0", got)
	}
}

func TestSendDoesNotRetryOnClientError(t *testing.T) {
	const bodyMarker = "not found body"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(bodyMarker))
	}))
	defer server.Close()

	logs := newLogSink(t)
	client := NewClient(server.URL, time.Second, 3, []int{0})

	err := client.Send(context.Background(), Payload{JobID: "job-1"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("error = %q, want the status", err.Error())
	}
	if strings.Contains(err.Error(), bodyMarker) {
		t.Fatalf("error embeds the response body: %q", err.Error())
	}
	if got := len(entriesForEvent(logs, "callback_retry")); got != 0 {
		t.Fatalf("callback_retry entries = %d, want 0 for a 4xx response", got)
	}
}
