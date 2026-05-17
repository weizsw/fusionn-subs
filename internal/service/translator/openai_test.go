package translator

import (
	"context"
	"os"
	"path/filepath"
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
		APIKey:         "sk-test",
		Model:          "gpt-4.1-mini",
		FallbackModels: []string{"gpt-5-mini"},
		APIBase:        "https://api.example.test/v1",
		UseHTTPX:       true,
		Instruction:    "Keep names.",
		RateLimit:      17,
		MaxBatchSize:   8,
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

func TestOpenAITranslatorBuildArgsCanUseFallbackModel(t *testing.T) {
	translator := NewOpenAITranslator(config.OpenAIConfig{
		APIKey:         "sk-test",
		Model:          "gpt-5-mini",
		FallbackModels: []string{"gpt-5-nano"},
	}, "Chinese", ".chs")

	req := Request{Job: types.JobMessage{
		JobID:        "job-1",
		VideoPath:    "/media/movie.mkv",
		SubtitlePath: "/subs/movie.eng.srt",
	}}

	args := buildOpenAIArgs(req, "/subs/movie.chs.srt", openAISnapshot{
		apiKey:         translator.apiKey,
		model:          "gpt-5-nano",
		targetLanguage: "Chinese",
		outputSuffix:   ".chs",
	})
	if !slices.Contains(args, "gpt-5-nano") {
		t.Fatalf("buildOpenAIArgs() missing fallback model: %#v", args)
	}
	if slices.Contains(args, "gpt-5-mini") {
		t.Fatalf("buildOpenAIArgs() included primary model while using fallback: %#v", args)
	}
}

func TestOpenAITranslatorTranslateTriesFallbackModelsBeforeFailing(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "gpt-subtrans.sh")
	attemptsPath := filepath.Join(dir, "attempts.txt")
	script := `#!/bin/sh
model=""
output=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -m) model="$2"; shift 2 ;;
    -o) output="$2"; shift 2 ;;
    *) shift ;;
  esac
done
echo "$model" >> "` + attemptsPath + `"
if [ "$model" = "gpt-5-mini" ]; then
  echo "failed to translate with primary" >&2
  exit 1
fi
printf "translated by %s" "$model" > "$output"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Setenv("OPENAI_SCRIPT_PATH", scriptPath)
	t.Setenv("LLM_SUBTRANS_DIR", dir)

	subtitlePath := filepath.Join(dir, "movie.eng.srt")
	if err := os.WriteFile(subtitlePath, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello.\n"), 0o600); err != nil {
		t.Fatalf("write subtitle: %v", err)
	}

	translator := NewOpenAITranslator(config.OpenAIConfig{
		APIKey:         "sk-test",
		Model:          "gpt-5-mini",
		FallbackModels: []string{"gpt-5-nano", "gpt-4.1-mini"},
	}, "Chinese", ".chs")

	out, err := translator.Translate(context.Background(), Request{Job: types.JobMessage{
		JobID:        "job-1",
		VideoPath:    filepath.Join(dir, "movie.mkv"),
		SubtitlePath: subtitlePath,
	}})
	if err != nil {
		t.Fatalf("Translate() unexpected error: %v", err)
	}
	wantOut := filepath.Join(dir, "movie.chs.srt")
	if out != wantOut {
		t.Fatalf("Translate() = %q, want %q", out, wantOut)
	}

	attempts, err := os.ReadFile(attemptsPath)
	if err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if string(attempts) != "gpt-5-mini\ngpt-5-nano\n" {
		t.Fatalf("attempts = %q", attempts)
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
		APIKey:         "sk-old",
		Model:          "gpt-old",
		FallbackModels: []string{"gpt-old-fallback"},
		APIBase:        "https://old.example.test/v1",
		UseHTTPX:       false,
		Instruction:    "Old instruction.",
		RateLimit:      3,
		MaxBatchSize:   2,
		Timeout:        5,
	}, "Chinese", ".chs")

	translator.UpdateFromConfig(&config.Config{
		OpenAI: config.OpenAIConfig{
			APIKey:         "sk-new",
			Model:          "gpt-5-mini",
			FallbackModels: []string{"gpt-5-nano"},
			APIBase:        "https://new.example.test/v1",
			UseHTTPX:       true,
			Instruction:    "New instruction.",
			MaxBatchSize:   7,
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
	if !reflect.DeepEqual(translator.fallbackModels, []string{"gpt-5-nano"}) {
		t.Fatalf("fallback models = %#v", translator.fallbackModels)
	}
}
