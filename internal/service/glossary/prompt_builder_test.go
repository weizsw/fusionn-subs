package glossary

import (
	"reflect"
	"testing"
	"time"
)

func TestBuildTerminologyPrefersMediaOverCommon(t *testing.T) {
	now := time.Now()
	entries := []PromptEntry{
		{
			Scope:          ScopeCommon,
			NormalizedTerm: "so15",
			DisplayTerm:    "SO15",
			TargetText:     "SO15 common",
			Confidence:     0.95,
			EvidenceCount:  4,
			Status:         StatusActive,
			Source:         SourcePromoted,
			LastSeenAt:     now,
		},
		{
			Scope:          ScopeMedia,
			MediaKey:       "tvdb:355620",
			NormalizedTerm: "so15",
			DisplayTerm:    "SO15",
			TargetText:     "SO15",
			Confidence:     0.90,
			EvidenceCount:  2,
			Status:         StatusActive,
			Source:         SourceGenerated,
			LastSeenAt:     now,
		},
	}

	got := BuildTerminology(entries, PromptOptions{
		MediaKey:            "tvdb:355620",
		InjectMinConfidence: 0.80,
		MaxPromptEntries:    10,
	})
	want := []Terminology{{Source: "SO15", Target: "SO15"}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("terminology = %#v, want %#v", got, want)
	}
}

func TestBuildTerminologyFiltersLowConfidence(t *testing.T) {
	got := BuildTerminology([]PromptEntry{
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

	if !reflect.DeepEqual(got, []Terminology(nil)) {
		t.Fatalf("terminology = %#v, want nil", got)
	}
}

func TestBuildTerminologyFallsBackToNormalizedTerm(t *testing.T) {
	got := BuildTerminology([]PromptEntry{
		{
			Scope:          ScopeCommon,
			NormalizedTerm: " central city ",
			TargetText:     "中城",
			Confidence:     0.90,
			Status:         StatusActive,
		},
	}, PromptOptions{InjectMinConfidence: 0.80, MaxPromptEntries: 10})
	want := []Terminology{{Source: "central city", Target: "中城"}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("terminology = %#v, want %#v", got, want)
	}
}
