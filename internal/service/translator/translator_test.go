package translator

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/fusionn-subs/internal/types"
)

func TestCombineInstructionsTrimsAndJoinsBaseAndExtra(t *testing.T) {
	got := combineInstructions("  Base instruction.  ", "\nExtra instruction.\t")
	want := "Base instruction.\n\nExtra instruction."

	if got != want {
		t.Fatalf("combineInstructions() = %q, want %q", got, want)
	}
}

func TestCombineInstructionsReturnsOnlyNonEmptyInstruction(t *testing.T) {
	tests := []struct {
		name  string
		base  string
		extra string
		want  string
	}{
		{
			name:  "base only",
			base:  "  Base instruction.  ",
			extra: " \n\t",
			want:  "Base instruction.",
		},
		{
			name:  "extra only",
			base:  " \n\t",
			extra: "  Extra instruction.  ",
			want:  "Extra instruction.",
		},
		{
			name:  "neither",
			base:  " \n\t",
			extra: "  ",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := combineInstructions(tt.base, tt.extra)
			if got != tt.want {
				t.Fatalf("combineInstructions() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFallbackTranslatorForwardsRequestUnchangedToFallbackProvider(t *testing.T) {
	wantReq := Request{
		Job: types.JobMessage{
			JobID:        "job-1",
			VideoPath:    "/media/movie.mkv",
			SubtitlePath: "/subs/movie.eng.srt",
			MediaTitle:   "Movie",
			MediaType:    "movie",
			ExternalIDs:  map[string]string{"tmdb": "123"},
		},
		ExtraInstruction: "Use glossary terms exactly.",
	}

	firstErr := errors.New("first provider failed")
	second := &capturingTranslator{returnValue: "/subs/movie.chs.srt"}
	fallback := &FallbackTranslator{translators: []namedTranslator{
		{name: "first", translator: failingTranslator{err: firstErr}},
		{name: "second", translator: second},
	}}

	got, err := fallback.Translate(context.Background(), wantReq)
	if err != nil {
		t.Fatalf("Translate() unexpected error: %v", err)
	}
	if got != second.returnValue {
		t.Fatalf("Translate() = %q, want %q", got, second.returnValue)
	}
	if !second.called {
		t.Fatal("fallback provider was not called")
	}
	if !reflect.DeepEqual(second.got, wantReq) {
		t.Fatalf("fallback request = %#v, want %#v", second.got, wantReq)
	}
}

type capturingTranslator struct {
	got         Request
	returnValue string
	called      bool
}

func (t *capturingTranslator) Translate(_ context.Context, req Request) (string, error) {
	t.got = req
	t.called = true
	return t.returnValue, nil
}

type failingTranslator struct {
	err error
}

func (t failingTranslator) Translate(_ context.Context, _ Request) (string, error) {
	return "", t.err
}
