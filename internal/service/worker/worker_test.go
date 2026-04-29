package worker

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/fusionn-subs/internal/service/translator"
	"github.com/fusionn-subs/internal/types"
)

type fakeTranslator struct {
	req translator.Request
	err error
}

func (f *fakeTranslator) Translate(_ context.Context, req translator.Request) (string, error) {
	f.req = req
	return "/tmp/out.chs.srt", f.err
}

type fakeGlossary struct {
	payload GlossaryPayload
	err     error
}

func (f fakeGlossary) Prepare(context.Context, types.JobMessage) (GlossaryPayload, error) {
	return f.payload, f.err
}

func TestTranslateJobPassesGlossaryTerminology(t *testing.T) {
	trans := &fakeTranslator{}
	terminology := []translator.Terminology{{Source: "SO15", Target: "SO15"}}
	w := &Worker{
		cfg:        Config{MaxTranslationRetries: 1},
		translator: trans,
		glossary: fakeGlossary{payload: GlossaryPayload{
			Terminology:         terminology,
			BuildTerminologyMap: true,
		}},
	}

	_, err := w.translateJob(context.Background(), types.JobMessage{
		JobID:        "job-1",
		VideoPath:    "/tmp/video.mkv",
		SubtitlePath: "/tmp/in.srt",
	})
	if err != nil {
		t.Fatalf("translate job: %v", err)
	}
	if !reflect.DeepEqual(trans.req.Terminology, terminology) {
		t.Fatalf("terminology = %#v, want %#v", trans.req.Terminology, terminology)
	}
	if !trans.req.BuildTerminologyMap {
		t.Fatal("expected build terminology map")
	}
	if trans.req.ExtraInstruction != "" {
		t.Fatalf("extra instruction = %q, want empty", trans.req.ExtraInstruction)
	}
}

func TestTranslateJobFailsOnGlossaryDBError(t *testing.T) {
	w := &Worker{
		cfg:        Config{MaxTranslationRetries: 1},
		translator: &fakeTranslator{},
		glossary:   fakeGlossary{err: errors.New("sqlite down")},
	}

	_, err := w.translateJob(context.Background(), types.JobMessage{
		JobID:        "job-1",
		VideoPath:    "/tmp/video.mkv",
		SubtitlePath: "/tmp/in.srt",
	})
	if err == nil {
		t.Fatal("expected glossary error")
	}
}
