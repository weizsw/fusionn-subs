package worker

import (
	"context"
	"errors"
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
	block string
	err   error
}

func (f fakeGlossary) Prepare(context.Context, types.JobMessage) (string, error) {
	return f.block, f.err
}

func TestTranslateJobPassesGlossaryInstruction(t *testing.T) {
	trans := &fakeTranslator{}
	w := &Worker{
		cfg:        Config{MaxTranslationRetries: 1},
		translator: trans,
		glossary:   fakeGlossary{block: "Glossary guidance:\n- SO15: keep as \"SO15\""},
	}

	_, err := w.translateJob(context.Background(), types.JobMessage{
		JobID:        "job-1",
		VideoPath:    "/tmp/video.mkv",
		SubtitlePath: "/tmp/in.srt",
	})
	if err != nil {
		t.Fatalf("translate job: %v", err)
	}
	if trans.req.ExtraInstruction == "" {
		t.Fatal("missing glossary instruction")
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
