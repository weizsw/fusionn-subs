package translator

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fusionn-subs/internal/config"
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
		Terminology: []Terminology{
			{Source: "Imperial", Target: "帝国"},
			{Source: "Guild", Target: "公会"},
		},
		BuildTerminologyMap: true,
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

func TestAppendTerminologyArgsAddsPairsAndBuildMap(t *testing.T) {
	args := appendTerminologyArgs([]string{"translate"}, Request{
		Terminology: []Terminology{
			{Source: " Imperial ", Target: " 帝国 "},
			{Source: "Guild", Target: "公会"},
		},
		BuildTerminologyMap: true,
	})

	want := []string{
		"translate",
		"--terminology", "Imperial::帝国",
		"--terminology", "Guild::公会",
		"--build-terminology-map",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("appendTerminologyArgs() = %#v, want %#v", args, want)
	}
}

func TestAppendTerminologyArgsSkipsBlankPairs(t *testing.T) {
	args := appendTerminologyArgs(nil, Request{
		Terminology: []Terminology{
			{Source: " ", Target: "帝国"},
			{Source: "Guild", Target: "\t"},
			{Source: " Fremen ", Target: " 弗雷曼 "},
		},
	})

	want := []string{"--terminology", "Fremen::弗雷曼"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("appendTerminologyArgs() = %#v, want %#v", args, want)
	}
}

func TestAppendTerminologyArgsOmitsBuildMapWhenDisabled(t *testing.T) {
	args := appendTerminologyArgs([]string{"translate"}, Request{
		Terminology: []Terminology{
			{Source: "Imperial", Target: "帝国"},
		},
	})

	want := []string{"translate", "--terminology", "Imperial::帝国"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("appendTerminologyArgs() = %#v, want %#v", args, want)
	}
}

func TestMaskAPIKeyInCommandMasksSeparatedShortAndLongFlags(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
	}{
		{name: "short", cmd: "/opt/script -k sk-short-secret -m model"},
		{name: "long", cmd: "/opt/script --apikey sk-long-secret --model model"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskAPIKeyInCommand(tt.cmd)
			if strings.Contains(got, "secret") {
				t.Fatalf("masked command contains raw secret: %s", got)
			}
			if got == tt.cmd {
				t.Fatalf("command was not masked: %s", got)
			}
		})
	}
}

func TestMaskAPIKeyInCommandMasksLongEqualsFlag(t *testing.T) {
	got := maskAPIKeyInCommand("/opt/script --apikey=sk-equals-secret --model model")
	if strings.Contains(got, "sk-equals-secret") {
		t.Fatalf("masked command contains raw secret: %s", got)
	}
	if got == "/opt/script --apikey=sk-equals-secret --model model" {
		t.Fatalf("command was not masked: %s", got)
	}
}

func TestOpenRouterBuildArgsIncludesTerminology(t *testing.T) {
	translator := NewOpenRouterTranslator(config.OpenRouterConfig{
		APIKey:       "sk-test",
		Model:        "openrouter/test-model",
		Instruction:  "Keep names.",
		RateLimit:    17,
		MaxBatchSize: 8,
	}, "Chinese", ".chs")

	req := terminologyArgsRequest()

	args := translator.buildArgs(req, "/subs/movie.chs.srt", "openrouter/test-model")
	assertTerminologyArgsSuffix(t, args)
}

func TestGeminiBuildArgsIncludesTerminology(t *testing.T) {
	translator := NewGeminiTranslator(context.Background(), config.GeminiConfig{
		APIKey:      "gemini-test",
		Instruction: "Keep names.",
		PrimaryModel: config.GeminiModelConfig{
			Name:         "gemini-test-model",
			RateLimit:    17,
			MaxBatchSize: 8,
		},
	}, "Chinese", ".chs")

	req := terminologyArgsRequest()

	args := translator.buildArgs(req, "/subs/movie.chs.srt", config.GeminiModelConfig{
		Name:         "gemini-test-model",
		RateLimit:    17,
		MaxBatchSize: 8,
	})
	assertTerminologyArgsSuffix(t, args)
}

func TestLocalLLMBuildArgsIncludesTerminology(t *testing.T) {
	req := terminologyArgsRequest()

	args := buildLocalLLMArgs(req, "/subs/movie.chs.srt", localLLMSnapshot{
		baseURL:        "http://localhost:11434",
		apiKey:         "local-test",
		model:          "local-model",
		endpoint:       config.DefaultOpenAIChatCompletionsEndpoint,
		instruction:    "Keep names.",
		rateLimit:      17,
		maxBatchSize:   8,
		timeout:        time.Minute,
		targetLanguage: "Chinese",
	})
	assertTerminologyArgsSuffix(t, args)
}

func terminologyArgsRequest() Request {
	return Request{
		Job: types.JobMessage{
			JobID:        "job-1",
			VideoPath:    "/media/movie.mkv",
			SubtitlePath: "/subs/movie.eng.srt",
			MediaTitle:   "Dune",
		},
		ExtraInstruction: "Prefer formal tone.",
		Terminology: []Terminology{
			{Source: " SO15 ", Target: " SO15 "},
		},
		BuildTerminologyMap: true,
	}
}

func assertTerminologyArgsSuffix(t *testing.T, args []string) {
	t.Helper()

	want := []string{"--terminology", "SO15::SO15", "--build-terminology-map"}
	if !slices.Equal(args[len(args)-len(want):], want) {
		t.Fatalf("args suffix = %#v, want %#v (full args: %#v)", args[len(args)-len(want):], want, args)
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
