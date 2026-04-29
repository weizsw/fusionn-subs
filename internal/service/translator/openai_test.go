package translator

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/types"
)

func TestOpenAITranslatorBuildArgsIncludesCustomInstance(t *testing.T) {
	t.Setenv("OPENAI_SCRIPT_PATH", "/tmp/gpt-subtrans.sh")
	t.Setenv("LLM_SUBTRANS_DIR", "/tmp/llm-subtrans")

	translator := NewOpenAITranslator(config.OpenAIConfig{
		APIKey:       "sk-test",
		Model:        "gpt-4.1-mini",
		APIBase:      "https://api.example.test/v1",
		UseHTTPX:     true,
		Instruction:  "Keep names.",
		RateLimit:    17,
		MaxBatchSize: 8,
	}, "Chinese", ".chs")

	req := Request{
		Job: types.JobMessage{
			JobID:        "job-1",
			VideoPath:    "/media/movie.mkv",
			SubtitlePath: "/subs/movie.eng.srt",
			MediaTitle:   "Dune",
		},
		ExtraInstruction: "Prefer formal tone.",
		Terminology: []Terminology{
			{Source: " Kwisatz Haderach ", Target: " 奎萨茨·哈德拉克 "},
			{Source: "Guild", Target: "公会"},
		},
		BuildTerminologyMap: true,
	}

	args := translator.buildArgs(req, "/subs/movie.chs.srt")
	want := []string{
		"/subs/movie.eng.srt",
		"-o", "/subs/movie.chs.srt",
		"-l", "Chinese",
		"-k", "sk-test",
		"-m", "gpt-4.1-mini",
		"-b", "https://api.example.test/v1",
		"--httpx",
		"--moviename", "Dune",
		"--instruction", "Keep names.\n\nPrefer formal tone.",
		"--ratelimit", "17",
		"--maxbatchsize", "8",
		"--terminology", "Kwisatz Haderach::奎萨茨·哈德拉克",
		"--terminology", "Guild::公会",
		"--build-terminology-map",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("buildArgs() = %#v, want %#v", args, want)
	}
}

func TestOpenAITranslatorBuildArgsOmitsKeyWhenUsingEnvironmentFallback(t *testing.T) {
	translator := NewOpenAITranslator(config.OpenAIConfig{
		Model: "gpt-4.1-mini",
	}, "Chinese", ".chs")

	req := Request{Job: types.JobMessage{
		JobID:        "job-1",
		VideoPath:    "/media/movie.mkv",
		SubtitlePath: "/subs/movie.eng.srt",
	}}

	args := translator.buildArgs(req, "/subs/movie.chs.srt")
	if slices.Contains(args, "-k") {
		t.Fatalf("buildArgs() unexpectedly included -k: %#v", args)
	}
}

func TestNewTranslatorSupportsOpenAIProvider(t *testing.T) {
	cfg := &config.Config{
		OpenAI: config.OpenAIConfig{
			APIKey: "sk-test",
			Model:  "gpt-4.1-mini",
		},
		Translator: config.TranslatorConfig{
			Providers:      []string{" " + config.ProviderOpenAI + " "},
			TargetLanguage: "Chinese",
			OutputSuffix:   ".chs",
		},
	}

	got, err := NewTranslator(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewTranslator() unexpected error: %v", err)
	}
	if _, ok := got.(*OpenAITranslator); !ok {
		t.Fatalf("NewTranslator() = %T, want *OpenAITranslator", got)
	}
}

func TestOpenAITranslatorUpdateFromConfigRefreshesSettings(t *testing.T) {
	translator := NewOpenAITranslator(config.OpenAIConfig{
		APIKey:       "sk-old",
		Model:        "gpt-old",
		APIBase:      "https://old.example.test/v1",
		UseHTTPX:     false,
		Instruction:  "Old instruction.",
		RateLimit:    3,
		MaxBatchSize: 2,
		Timeout:      5,
	}, "Chinese", ".chs")

	translator.UpdateFromConfig(&config.Config{
		OpenAI: config.OpenAIConfig{
			APIKey:       "sk-new",
			Model:        "gpt-5-mini",
			APIBase:      "https://new.example.test/v1",
			UseHTTPX:     true,
			Instruction:  "New instruction.",
			MaxBatchSize: 7,
		},
	})

	req := Request{Job: types.JobMessage{
		JobID:        "job-1",
		VideoPath:    "/media/movie.mkv",
		SubtitlePath: "/subs/movie.eng.srt",
	}}
	args := translator.buildArgs(req, "/subs/movie.chs.srt")
	want := []string{
		"/subs/movie.eng.srt",
		"-o", "/subs/movie.chs.srt",
		"-l", "Chinese",
		"-k", "sk-new",
		"-m", "gpt-5-mini",
		"-b", "https://new.example.test/v1",
		"--httpx",
		"--instruction", "New instruction.",
		"--ratelimit", "10",
		"--maxbatchsize", "7",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("buildArgs() = %#v, want %#v", args, want)
	}
	if translator.timeout != config.DefaultOpenAITimeout {
		t.Fatalf("timeout = %v, want %v", translator.timeout, config.DefaultOpenAITimeout)
	}
}
