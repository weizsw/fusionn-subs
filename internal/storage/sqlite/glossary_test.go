package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fusionn-subs/internal/service/glossary"
	"github.com/fusionn-subs/internal/types"
)

func TestGlossaryStoreUpsertMergesSameTarget(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "glossary.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	store := NewGlossaryStore(db)
	req := glossary.UpsertRequest{
		Job:            types.JobMessage{JobID: "job-1", SubtitlePath: "/tmp/e1.srt"},
		MediaKey:       "tvdb:355620",
		TargetLanguage: "zh-Hans",
		Options: glossary.UpsertOptions{
			MinConfidence:             0.75,
			MaxActiveVariantsPerTerm:  3,
			MaxObservationsPerVariant: 10,
		},
		Entries: []glossary.GeneratedEntry{{
			SourceTerm:      "SO15",
			NormalizedTerm:  "so15",
			TargetLanguage:  "zh-Hans",
			TargetText:      "SO15",
			Definition:      "counter terrorism command",
			TranslationMode: glossary.TranslationModePreserve,
			Category:        glossary.CategoryOrganization,
			Confidence:      0.91,
			Evidence:        []string{"SO15 asked DCI Carey."},
		}},
	}

	first, err := store.UpsertGeneratedEntries(ctx, req)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second, err := store.UpsertGeneratedEntries(ctx, req)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if first.Created != 1 || second.Merged != 1 {
		t.Fatalf("results = %#v %#v", first, second)
	}

	entries, err := store.LoadPromptEntries(ctx, "tvdb:355620", "zh-Hans")
	if err != nil {
		t.Fatalf("load prompt entries: %v", err)
	}
	if len(entries) != 1 || entries[0].EvidenceCount != 2 {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestGlossaryStoreSuppressesExcessActiveVariants(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "glossary.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	store := NewGlossaryStore(db)
	result, err := store.UpsertGeneratedEntries(ctx, glossary.UpsertRequest{
		Job:            types.JobMessage{JobID: "job-1", SubtitlePath: "/tmp/e1.srt"},
		MediaKey:       "tvdb:355620",
		TargetLanguage: "zh-Hans",
		Options: glossary.UpsertOptions{
			MinConfidence:             0.75,
			MaxActiveVariantsPerTerm:  1,
			MaxObservationsPerVariant: 10,
		},
		Entries: []glossary.GeneratedEntry{
			{
				SourceTerm:      "SO15",
				NormalizedTerm:  "so15",
				TargetText:      "反恐指挥部",
				Definition:      "translated form",
				TranslationMode: glossary.TranslationModeTranslate,
				Category:        glossary.CategoryOrganization,
				Confidence:      0.80,
			},
			{
				SourceTerm:      "SO15",
				NormalizedTerm:  "so15",
				TargetText:      "SO15",
				Definition:      "preserved acronym",
				TranslationMode: glossary.TranslationModePreserve,
				Category:        glossary.CategoryOrganization,
				Confidence:      0.95,
			},
		},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if result.Suppressed != 1 {
		t.Fatalf("suppressed = %d, want 1; result = %#v", result.Suppressed, result)
	}

	entries, err := store.LoadPromptEntries(ctx, "tvdb:355620", "zh-Hans")
	if err != nil {
		t.Fatalf("load prompt entries: %v", err)
	}
	if len(entries) != 1 || entries[0].TargetText != "SO15" {
		t.Fatalf("entries = %#v", entries)
	}
}
