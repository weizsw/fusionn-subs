package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/fusionn-subs/internal/client/callback"
	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/service/translator"
	"github.com/fusionn-subs/internal/types"
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

// sequenceTranslator fails with the given errors in order, then succeeds.
type sequenceTranslator struct {
	errs []error
}

func (s *sequenceTranslator) Translate(context.Context, translator.Request) (string, error) {
	if len(s.errs) == 0 {
		return "/subs/movie.chs.srt", nil
	}
	err := s.errs[0]
	s.errs = s.errs[1:]
	return "", err
}

func testJobMessage() types.JobMessage {
	return types.JobMessage{
		JobID:        "job-1",
		VideoPath:    "/media/movie.mkv",
		SubtitlePath: "/subs/movie.eng.srt",
		MediaType:    "movie",
	}
}

func unreachableCallbackClient() *callback.Client {
	return callback.NewClient("http://127.0.0.1:1/callback", time.Second, 0, []int{1})
}

func healthyCallbackServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestHandleMessageLogsOneFailureWithStageAndRetryWarnings(t *testing.T) {
	logs := newLogSink(t)
	providerErr := errors.New("provider timeout")
	w := &Worker{
		cfg: Config{
			MaxTranslationRetries: 3,
			TranslationRetryDelay: time.Millisecond,
		},
		translator: &sequenceTranslator{errs: []error{providerErr, providerErr, providerErr}},
		callback:   unreachableCallbackClient(),
	}

	w.handleMessage(context.Background(), testJobMessage())

	failed := entriesForEvent(logs, "job_failed")
	if len(failed) != 1 {
		t.Fatalf("job_failed entries = %d, want 1", len(failed))
	}
	if got := countErrors(logs); got != 1 {
		t.Fatalf("ERROR entries = %d, want 1", got)
	}

	failedContext := failed[0].ContextMap()
	if failedContext["stage"] != "translation" {
		t.Fatalf("stage = %v, want translation", failedContext["stage"])
	}
	if failedContext["job_id"] != "job-1" {
		t.Fatalf("job_id = %v, want job-1", failedContext["job_id"])
	}
	if got, ok := failedContext["error"].(string); !ok || !strings.Contains(got, providerErr.Error()) {
		t.Fatalf("error = %v, want it to carry %q", failedContext["error"], providerErr.Error())
	}

	retries := entriesForEvent(logs, "translation_retry")
	if len(retries) != 2 {
		t.Fatalf("translation_retry entries = %d, want 2", len(retries))
	}
	for _, entry := range retries {
		if entry.Level != zapcore.WarnLevel {
			t.Fatalf("translation_retry level = %v, want WARN", entry.Level)
		}
		if entry.ContextMap()["retry_after_ms"] == nil {
			t.Fatal("translation_retry is missing retry_after_ms")
		}
	}
}

func TestHandleMessageLogsCallbackStageFailure(t *testing.T) {
	logs := newLogSink(t)
	w := &Worker{
		cfg:        Config{MaxTranslationRetries: 1},
		translator: &sequenceTranslator{},
		callback:   unreachableCallbackClient(),
	}

	w.handleMessage(context.Background(), testJobMessage())

	failed := entriesForEvent(logs, "job_failed")
	if len(failed) != 1 {
		t.Fatalf("job_failed entries = %d, want 1", len(failed))
	}
	if stage := failed[0].ContextMap()["stage"]; stage != "callback" {
		t.Fatalf("stage = %v, want callback", stage)
	}
	if got := countErrors(logs); got != 1 {
		t.Fatalf("ERROR entries = %d, want 1", got)
	}
}

func TestHandleMessageLogsJobCompleted(t *testing.T) {
	logs := newLogSink(t)
	w := &Worker{
		cfg:        Config{MaxTranslationRetries: 1},
		translator: &sequenceTranslator{},
		callback:   callback.NewClient(healthyCallbackServer(t).URL, time.Second, 0, []int{1}),
	}

	w.handleMessage(context.Background(), testJobMessage())

	completed := entriesForEvent(logs, "job_completed")
	if len(completed) != 1 {
		t.Fatalf("job_completed entries = %d, want 1", len(completed))
	}
	if got := countErrors(logs); got != 0 {
		t.Fatalf("ERROR entries = %d, want 0", got)
	}
}

func TestHandleMessageLogsRecoveryAfterRetry(t *testing.T) {
	logs := newLogSink(t)
	w := &Worker{
		cfg: Config{
			MaxTranslationRetries: 3,
			TranslationRetryDelay: time.Millisecond,
		},
		translator: &sequenceTranslator{errs: []error{errors.New("provider timeout")}},
		callback:   callback.NewClient(healthyCallbackServer(t).URL, time.Second, 0, []int{1}),
	}

	w.handleMessage(context.Background(), testJobMessage())

	if got := len(entriesForEvent(logs, "translation_retry")); got != 1 {
		t.Fatalf("translation_retry entries = %d, want 1", got)
	}
	if got := len(entriesForEvent(logs, "translation_recovered")); got != 1 {
		t.Fatalf("translation_recovered entries = %d, want 1", got)
	}
	if got := countErrors(logs); got != 0 {
		t.Fatalf("ERROR entries = %d, want 0", got)
	}
}

func TestHandleMessageSkipsRetryDelayForRateLimited(t *testing.T) {
	logs := newLogSink(t)
	w := &Worker{
		cfg: Config{
			MaxTranslationRetries: 2,
			TranslationRetryDelay: time.Hour, // must never be waited on
		},
		translator: &sequenceTranslator{errs: []error{translator.ErrRateLimited}},
		callback:   unreachableCallbackClient(),
	}

	started := time.Now()
	w.handleMessage(context.Background(), testJobMessage())
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("rate limited retry waited %v, want no delay", elapsed)
	}

	retries := entriesForEvent(logs, "translation_retry")
	if len(retries) != 1 {
		t.Fatalf("translation_retry entries = %d, want 1", len(retries))
	}
	if _, present := retries[0].ContextMap()["retry_after_ms"]; present {
		t.Fatal("rate limited retry must not advertise a retry delay")
	}
}

// TestHandleMessageCarriesScriptFailureReasonIntoFinalLog runs a real tool
// process that exits non-zero, so the failure reason has to travel from the
// drained stream to the single worker failure record.
func TestHandleMessageCarriesScriptFailureReasonIntoFinalLog(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-subtrans.sh")
	scriptBody := "#!/bin/sh\necho 'ModuleNotFoundError: No module named llm_subtrans' >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		t.Fatalf("write fake script: %v", err)
	}
	t.Setenv("LLM_SUBTRANS_SCRIPT_PATH", script)
	t.Setenv("LLM_SUBTRANS_DIR", dir)

	w := &Worker{
		cfg: Config{MaxTranslationRetries: 1},
		translator: translator.NewLocalLLMTranslator(config.LocalLLMConfig{
			BaseURL:      "http://localhost:11434",
			Model:        "local-model",
			Endpoint:     config.DefaultOpenAIChatCompletionsEndpoint,
			Timeout:      10 * time.Second,
			MaxBatchSize: 1,
		}, "Chinese", ".chs"),
		callback: unreachableCallbackClient(),
	}

	logs := newLogSink(t)
	w.handleMessage(context.Background(), testJobMessage())

	failed := entriesForEvent(logs, "job_failed")
	if len(failed) != 1 {
		t.Fatalf("job_failed entries = %d, want 1", len(failed))
	}
	errorText, _ := failed[0].ContextMap()["error"].(string)
	if !strings.Contains(errorText, "missing module") {
		t.Fatalf("job_failed error = %q, want the safe script failure reason", errorText)
	}
	if strings.Contains(errorText, "llm_subtrans") {
		t.Fatalf("job_failed error embeds raw tool output: %q", errorText)
	}
}

// echoTranslator returns an output path derived from the request, so a log
// entry can be traced back to the job that produced it.
type echoTranslator struct{}

func (echoTranslator) Translate(_ context.Context, req translator.Request) (string, error) {
	return strings.Replace(req.Job.SubtitlePath, ".eng.srt", ".chs.srt", 1), nil
}

func TestConcurrentJobsDoNotMixContext(t *testing.T) {
	logs := newLogSink(t)
	w := &Worker{
		cfg:        Config{MaxTranslationRetries: 1},
		translator: echoTranslator{},
		callback:   callback.NewClient(healthyCallbackServer(t).URL, time.Second, 0, []int{1}),
	}

	messages := []types.JobMessage{
		{JobID: "job-a", VideoPath: "/media/a.mkv", SubtitlePath: "/subs/a.eng.srt", MediaType: "movie"},
		{JobID: "job-b", VideoPath: "/media/b.mkv", SubtitlePath: "/subs/b.eng.srt", MediaType: "movie"},
	}

	var wg sync.WaitGroup
	for _, msg := range messages {
		wg.Add(1)
		go func(message types.JobMessage) {
			defer wg.Done()
			w.handleMessage(context.Background(), message)
		}(msg)
	}
	wg.Wait()

	expectedSubtitle := map[string]string{
		"job-a": "/subs/a.eng.srt",
		"job-b": "/subs/b.eng.srt",
	}

	counts := map[string]int{}
	for _, entry := range logs.All() {
		context := entry.ContextMap()
		jobID, _ := context["job_id"].(string)
		if jobID == "" {
			continue
		}
		wantSubtitle, known := expectedSubtitle[jobID]
		if !known {
			t.Fatalf("unexpected job_id %q", jobID)
		}
		counts[fmt.Sprint(context["event"])]++

		switch context["event"] {
		case "job_received":
			if context["subtitle_path"] != wantSubtitle {
				t.Fatalf("job_received for %s carries subtitle_path %v, want %s", jobID, context["subtitle_path"], wantSubtitle)
			}
		case "job_completed":
			if context["chs_subtitle_path"] != strings.Replace(wantSubtitle, ".eng.srt", ".chs.srt", 1) {
				t.Fatalf("job_completed for %s carries chs_subtitle_path %v", jobID, context["chs_subtitle_path"])
			}
		}
	}

	if counts["job_received"] != 2 || counts["job_completed"] != 2 {
		t.Fatalf("event counts = %v, want 2 job_received and 2 job_completed", counts)
	}
	if got := countErrors(logs); got != 0 {
		t.Fatalf("ERROR entries = %d, want 0", got)
	}
}

// TestSampleLogLines renders real worker events through the shared logger so a
// reviewer can read the exact lines the service emits.
func TestSampleLogLines(t *testing.T) {
	t.Setenv("LOG_LEVEL", "DEBUG")

	var buf bytes.Buffer
	previous := logger.Log
	t.Cleanup(func() { logger.Log = previous })
	logger.Configure(logger.Config{Output: zapcore.AddSync(&buf)})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// A normal job that needs one translation retry.
	worker := &Worker{
		cfg:        Config{MaxTranslationRetries: 3, TranslationRetryDelay: time.Millisecond},
		translator: &sequenceTranslator{errs: []error{errors.New("provider timeout")}},
		callback:   callback.NewClient(server.URL, time.Second, 0, []int{1}),
	}
	worker.handleMessage(context.Background(), types.JobMessage{
		JobID:        "job-7f3c",
		VideoPath:    "/media/Show.S01E01.mkv",
		SubtitlePath: "/data/Show.S01E01.eng.srt",
		MediaType:    "episode",
	})

	// A job that fails after its only attempt.
	failing := &Worker{
		cfg:        Config{MaxTranslationRetries: 1},
		translator: &sequenceTranslator{errs: []error{errors.New("provider timeout")}},
		callback:   unreachableCallbackClient(),
	}
	failing.handleMessage(context.Background(), types.JobMessage{
		JobID:        "job-9b21",
		VideoPath:    "/media/Movie.mkv",
		SubtitlePath: "/data/Movie.eng.srt",
		MediaType:    "movie",
	})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	prefix := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}\+08:00 (DEBUG|INFO|WARN|ERROR) service=fusionn-subs component=\S+ event=\S+ `)
	for _, line := range lines {
		if !prefix.MatchString(line) {
			t.Fatalf("unexpected log line: %s", line)
		}
		t.Log(line)
	}
	if len(lines) != 7 {
		t.Fatalf("log lines = %d, want 7", len(lines))
	}
}
