package glossary

import (
	"strings"
	"testing"
	"time"
)

func TestBuildPromptPrefersMediaOverCommon(t *testing.T) {
	now := time.Now()
	entries := []PromptEntry{
		{
			Scope:          ScopeCommon,
			NormalizedTerm: "so15",
			DisplayTerm:    "SO15",
			TargetText:     "SO15 common",
			Definition:     "Common meaning",
			Confidence:     0.95,
			EvidenceCount:  4,
			Status:         StatusActive,
			Source:         SourcePromoted,
			LastSeenAt:     now,
		},
		{
			Scope:           ScopeMedia,
			MediaKey:        "tvdb:355620",
			NormalizedTerm:  "so15",
			DisplayTerm:     "SO15",
			TargetText:      "SO15",
			Definition:      "The Capture usage",
			TranslationMode: TranslationModePreserve,
			Confidence:      0.90,
			EvidenceCount:   2,
			Status:          StatusActive,
			Source:          SourceGenerated,
			LastSeenAt:      now,
		},
	}

	got := BuildPrompt(entries, PromptOptions{
		MediaKey:            "tvdb:355620",
		InjectMinConfidence: 0.80,
		MaxPromptEntries:    10,
	})

	if !strings.Contains(got, `SO15: keep as "SO15"`) {
		t.Fatalf("prompt = %s", got)
	}
	if strings.Contains(got, "Common meaning") {
		t.Fatalf("common entry should not win: %s", got)
	}
}

func TestBuildPromptFiltersLowConfidence(t *testing.T) {
	got := BuildPrompt([]PromptEntry{
		{
			Scope:          ScopeMedia,
			MediaKey:       "m",
			NormalizedTerm: "x",
			DisplayTerm:    "X",
			TargetText:     "X",
			Confidence:     0.40,
			Status:         StatusActive,
		},
	}, PromptOptions{MediaKey: "m", InjectMinConfidence: 0.80, MaxPromptEntries: 10})

	if got != "" {
		t.Fatalf("prompt = %q", got)
	}
}
