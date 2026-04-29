package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestGlossaryStorePromotesCommonEntryAcrossMedia(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "glossary.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	store := NewGlossaryStore(db)
	for _, mediaKey := range []string{"tvdb:1", "tvdb:2", "tvdb:3"} {
		_, err := store.UpsertGeneratedEntries(ctx, glossary.UpsertRequest{
			Job:            types.JobMessage{JobID: "job-" + mediaKey, SubtitlePath: "/tmp/" + mediaKey + ".srt"},
			MediaKey:       mediaKey,
			TargetLanguage: "zh-Hans",
			Options: glossary.UpsertOptions{
				MinConfidence:             0.75,
				MaxActiveVariantsPerTerm:  3,
				MaxObservationsPerVariant: 10,
			},
			Entries: []glossary.GeneratedEntry{{
				SourceTerm:      "SO15",
				NormalizedTerm:  "so15",
				TargetText:      "SO15",
				Definition:      "counter terrorism command",
				TranslationMode: glossary.TranslationModePreserve,
				Category:        glossary.CategoryOrganization,
				Confidence:      0.91,
			}},
		})
		if err != nil {
			t.Fatalf("upsert %s: %v", mediaKey, err)
		}
	}

	result, err := store.PromoteCommonEntries(ctx, glossary.PromotionOptions{
		TargetLanguage:        "zh-Hans",
		MinConfidence:         0.85,
		MinDistinctMediaCount: 3,
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if result.Promoted != 1 {
		t.Fatalf("promotion result = %#v", result)
	}

	entries, err := store.LoadPromptEntries(ctx, "tvdb:other", "zh-Hans")
	if err != nil {
		t.Fatalf("load prompt entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Scope != glossary.ScopeCommon || entries[0].Source != glossary.SourcePromoted {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestGlossaryStoreTimestampsUseLocalTime(t *testing.T) {
	loc := time.FixedZone("TEST_LOCAL", 3*60*60)
	previousLocal := time.Local
	time.Local = loc
	t.Cleanup(func() {
		time.Local = previousLocal
	})

	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "glossary.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	store := NewGlossaryStore(db)
	_, err = store.UpsertGeneratedEntries(ctx, glossary.UpsertRequest{
		Job:            types.JobMessage{JobID: "job-local-time", SubtitlePath: "/tmp/e1.srt"},
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
			TargetText:      "SO15",
			Definition:      "counter terrorism command",
			TranslationMode: glossary.TranslationModePreserve,
			Category:        glossary.CategoryOrganization,
			Confidence:      0.91,
		}},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := store.RecordJob(ctx, glossary.JobRecord{
		JobID:            "job-local-time",
		MediaKey:         "tvdb:355620",
		SubtitlePathHash: "subtitle-hash",
		Status:           glossary.JobStatusCompleted,
	}); err != nil {
		t.Fatalf("record job: %v", err)
	}

	for name, query := range map[string]string{
		"term created_at":        "select created_at from glossary_terms limit 1",
		"variant created_at":     "select created_at from glossary_variants limit 1",
		"observation created_at": "select created_at from glossary_observations limit 1",
		"job created_at":         "select created_at from glossary_jobs where job_id = 'job-local-time'",
		"job completed_at":       "select completed_at from glossary_jobs where job_id = 'job-local-time'",
	} {
		var value string
		if err := db.QueryRowContext(ctx, query).Scan(&value); err != nil {
			t.Fatalf("%s query: %v", name, err)
		}
		assertRecentLocalTimestamp(t, name, value, loc)
	}
}

func assertRecentLocalTimestamp(t *testing.T, name, value string, loc *time.Location) {
	t.Helper()

	wallClock := strings.ReplaceAll(value, "T", " ")
	if len(wallClock) >= len("2006-01-02 15:04:05") {
		wallClock = wallClock[:len("2006-01-02 15:04:05")]
	}
	got, err := time.ParseInLocation("2006-01-02 15:04:05", wallClock, loc)
	if err != nil {
		t.Fatalf("%s = %q is not a SQLite timestamp: %v", name, value, err)
	}
	now := time.Now().In(loc)
	if got.Before(now.Add(-2*time.Minute)) || got.After(now.Add(2*time.Minute)) {
		t.Fatalf("%s = %s, want within 2m of local time %s", name, value, now.Format("2006-01-02 15:04:05"))
	}
}
