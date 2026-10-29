package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/fusionn-subs/internal/service/translator"
)

// panickingTranslator stands in for a translator bug.
type panickingTranslator struct{}

func (panickingTranslator) Translate(context.Context, translator.Request) (string, error) {
	panic("translator bug")
}

// fakeQueue serves one message, then reports an empty queue, and records the
// messages pushed to each key.
type fakeQueue struct {
	mu     sync.Mutex
	raw    string
	served bool
	pushed map[string][]string
}

func (q *fakeQueue) BRPop(ctx context.Context, _ time.Duration, keys ...string) *redis.StringSliceCmd {
	q.mu.Lock()
	defer q.mu.Unlock()
	cmd := redis.NewStringSliceCmd(ctx)
	if q.served {
		cmd.SetErr(redis.Nil)
		return cmd
	}
	q.served = true
	cmd.SetVal([]string{keys[0], q.raw})
	return cmd
}

func (q *fakeQueue) LPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pushed == nil {
		q.pushed = map[string][]string{}
	}
	for _, value := range values {
		q.pushed[key] = append(q.pushed[key], fmt.Sprint(value))
	}
	cmd := redis.NewIntCmd(ctx)
	cmd.SetVal(int64(len(q.pushed[key])))
	return cmd
}

func queuedMessage(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(testJobMessage())
	if err != nil {
		t.Fatal(err)
	}
	// An unknown field must survive: replay pushes back the exact original.
	return strings.Replace(string(raw), "{", `{"future_field":"kept",`, 1)
}

// A panic fails the job once, with its stack, instead of ending the process;
// the consumed message is kept for replay.
func TestPanickingJobFailsOnceAndIsDeadLettered(t *testing.T) {
	logs := newLogSink(t)
	queue := &fakeQueue{raw: queuedMessage(t)}
	w := &Worker{
		redis:      queue,
		cfg:        Config{Queue: "translate_queue", MaxTranslationRetries: 1},
		translator: panickingTranslator{},
		callback:   unreachableCallbackClient(),
	}

	if err := w.processNext(context.Background()); err != nil {
		t.Fatalf("processNext: %v", err)
	}

	failed := entriesForEvent(logs, "job_failed")
	if len(failed) != 1 || countErrors(logs) != 1 {
		t.Fatalf("job_failed entries = %d, ERROR entries = %d, want 1 and 1", len(failed), countErrors(logs))
	}
	fields := failed[0].ContextMap()
	if fields["stage"] != "translation" || fields["job_id"] != "job-1" || fields["error"] != "panic: translator bug" {
		t.Errorf("job_failed fields = %v", fields)
	}
	if stack, _ := fields["stack"].(string); !strings.Contains(stack, "panickingTranslator") {
		t.Errorf("job_failed stack lacks the panicking frame: %q", stack)
	}
	if got := queue.pushed["translate_queue:dead"]; len(got) != 1 || got[0] != queue.raw {
		t.Errorf("dead-lettered = %q, want the original message", got)
	}
	lettered := entriesForEvent(logs, "job_dead_lettered")
	if len(lettered) != 1 || lettered[0].ContextMap()["job_id"] != "job-1" {
		t.Errorf("job_dead_lettered entries = %v", lettered)
	}
}

// An ordinary failure keeps today's behavior: logged once, not dead-lettered.
func TestFailedJobIsNotDeadLettered(t *testing.T) {
	newLogSink(t)
	queue := &fakeQueue{raw: queuedMessage(t)}
	w := &Worker{
		redis:      queue,
		cfg:        Config{Queue: "translate_queue", MaxTranslationRetries: 1},
		translator: &sequenceTranslator{errs: []error{errors.New("provider timeout")}},
		callback:   unreachableCallbackClient(),
	}

	if err := w.processNext(context.Background()); err != nil {
		t.Fatalf("processNext: %v", err)
	}
	if len(queue.pushed) != 0 {
		t.Errorf("ordinary failure was dead-lettered: %v", queue.pushed)
	}
}
