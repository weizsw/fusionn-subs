package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fusionn-subs/internal/service/glossary"
)

type GlossaryStore struct {
	db *sql.DB
}

var _ glossary.Store = (*GlossaryStore)(nil)

func NewGlossaryStore(db *sql.DB) *GlossaryStore {
	return &GlossaryStore{db: db}
}

func (s *GlossaryStore) LoadPromptEntries(ctx context.Context, mediaKey, targetLanguage string) ([]glossary.PromptEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
select t.scope, coalesce(t.media_key, ''), t.normalized_term, t.display_term,
       v.target_language, v.target_text, v.definition, v.translation_mode,
       v.category, v.status, v.source, v.confidence, v.evidence_count, v.last_seen_at
from glossary_terms t
join glossary_variants v on v.term_id = t.id
where v.target_language = ?
  and v.status = 'active'
  and (t.scope = 'common' or (t.scope = 'media' and t.media_key = ?))
`, targetLanguage, mediaKey)
	if err != nil {
		return nil, fmt.Errorf("load prompt entries: %w", err)
	}
	defer rows.Close()

	var out []glossary.PromptEntry
	for rows.Next() {
		var e glossary.PromptEntry
		var scope, mode, category, status, source, lastSeen string
		if err := rows.Scan(
			&scope,
			&e.MediaKey,
			&e.NormalizedTerm,
			&e.DisplayTerm,
			&e.TargetLanguage,
			&e.TargetText,
			&e.Definition,
			&mode,
			&category,
			&status,
			&source,
			&e.Confidence,
			&e.EvidenceCount,
			&lastSeen,
		); err != nil {
			return nil, fmt.Errorf("scan prompt entry: %w", err)
		}
		e.Scope = glossary.Scope(scope)
		e.TranslationMode = glossary.TranslationMode(mode)
		e.Category = glossary.Category(category)
		e.Status = glossary.VariantStatus(status)
		e.Source = glossary.VariantSource(source)
		e.LastSeenAt = parseSQLiteTime(lastSeen)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prompt entries: %w", err)
	}
	return out, nil
}

func (s *GlossaryStore) UpsertGeneratedEntries(ctx context.Context, req glossary.UpsertRequest) (glossary.UpsertResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return glossary.UpsertResult{}, fmt.Errorf("begin glossary upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var result glossary.UpsertResult
	for _, entry := range req.Entries {
		termID, err := upsertTerm(ctx, tx, req.MediaKey, entry)
		if err != nil {
			return result, err
		}

		status := glossary.StatusActive
		if entry.Confidence < req.Options.MinConfidence {
			status = glossary.StatusCandidate
			result.Candidates++
		}

		targetLanguage := strings.TrimSpace(req.TargetLanguage)
		if targetLanguage == "" {
			targetLanguage = strings.TrimSpace(entry.TargetLanguage)
		}

		var variantID int64
		err = tx.QueryRowContext(ctx, `
select id from glossary_variants
where term_id = ? and target_language = ? and lower(target_text) = lower(?)
limit 1`, termID, targetLanguage, entry.TargetText).Scan(&variantID)
		switch {
		case err == nil:
			_, err = tx.ExecContext(ctx, `
update glossary_variants
set confidence = max(confidence, ?),
    evidence_count = evidence_count + 1,
    definition = case when length(definition) >= length(?) then definition else ? end,
    updated_at = current_timestamp,
    last_seen_at = current_timestamp
where id = ?`, entry.Confidence, entry.Definition, entry.Definition, variantID)
			if err != nil {
				return result, fmt.Errorf("merge glossary variant: %w", err)
			}
			result.Merged++
		case errors.Is(err, sql.ErrNoRows):
			res, err := tx.ExecContext(ctx, `
insert into glossary_variants(term_id, target_language, target_text, definition, translation_mode, category, status, source, confidence, evidence_count)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
				termID,
				targetLanguage,
				entry.TargetText,
				entry.Definition,
				entry.TranslationMode,
				entry.Category,
				status,
				glossary.SourceGenerated,
				entry.Confidence,
			)
			if err != nil {
				return result, fmt.Errorf("insert glossary variant: %w", err)
			}
			variantID, _ = res.LastInsertId()
			if status == glossary.StatusActive {
				result.Created++
			}
		default:
			return result, fmt.Errorf("find glossary variant: %w", err)
		}

		if err := insertObservation(ctx, tx, variantID, req, entry); err != nil {
			return result, err
		}
		suppressed, err := suppressExcessActiveVariants(ctx, tx, termID, targetLanguage, req.Options.MaxActiveVariantsPerTerm)
		if err != nil {
			return result, err
		}
		result.Suppressed += suppressed
	}

	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit glossary upsert: %w", err)
	}
	return result, nil
}

func (s *GlossaryStore) RecordJob(ctx context.Context, job glossary.JobRecord) error {
	_, err := s.db.ExecContext(ctx, `
insert into glossary_jobs(job_id, media_key, subtitle_path_hash, status, error, completed_at)
values (?, ?, ?, ?, ?, current_timestamp)
on conflict(job_id) do update set
    media_key = excluded.media_key,
    subtitle_path_hash = excluded.subtitle_path_hash,
    status = excluded.status,
    error = excluded.error,
    completed_at = current_timestamp`, job.JobID, job.MediaKey, job.SubtitlePathHash, job.Status, job.Error)
	if err != nil {
		return fmt.Errorf("record glossary job: %w", err)
	}
	return nil
}

func (s *GlossaryStore) PromoteCommonEntries(ctx context.Context, opts glossary.PromotionOptions) (glossary.PromotionResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return glossary.PromotionResult{}, fmt.Errorf("begin glossary promotion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
select t.normalized_term, min(t.display_term), v.target_text, max(v.definition),
       v.translation_mode, v.category, avg(v.confidence), sum(v.evidence_count)
from glossary_terms t
join glossary_variants v on v.term_id = t.id
where t.scope = 'media'
  and v.target_language = ?
  and v.status = 'active'
  and v.confidence >= ?
group by t.normalized_term, v.target_text, v.translation_mode, v.category
having count(distinct t.media_key) >= ?`, opts.TargetLanguage, opts.MinConfidence, opts.MinDistinctMediaCount)
	if err != nil {
		return glossary.PromotionResult{}, fmt.Errorf("select promotion candidates: %w", err)
	}
	defer rows.Close()

	result := glossary.PromotionResult{}
	for rows.Next() {
		var normalized, display, targetText, definition, mode, category string
		var confidence float64
		var evidenceCount int
		if err := rows.Scan(&normalized, &display, &targetText, &definition, &mode, &category, &confidence, &evidenceCount); err != nil {
			return result, fmt.Errorf("scan promotion candidate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
insert or ignore into glossary_terms(scope, media_key, normalized_term, display_term)
values ('common', null, ?, ?)`, normalized, display); err != nil {
			return result, fmt.Errorf("insert common term: %w", err)
		}

		var termID int64
		if err := tx.QueryRowContext(ctx, `
select id from glossary_terms where scope = 'common' and normalized_term = ?`, normalized).Scan(&termID); err != nil {
			return result, fmt.Errorf("load common term id: %w", err)
		}

		res, err := tx.ExecContext(ctx, `
insert into glossary_variants(term_id, target_language, target_text, definition, translation_mode, category, status, source, confidence, evidence_count)
select ?, ?, ?, ?, ?, ?, 'active', 'promoted', ?, ?
where not exists (
    select 1 from glossary_variants
    where term_id = ? and target_language = ? and lower(target_text) = lower(?)
)`,
			termID,
			opts.TargetLanguage,
			targetText,
			definition,
			mode,
			category,
			confidence,
			evidenceCount,
			termID,
			opts.TargetLanguage,
			targetText,
		)
		if err != nil {
			return result, fmt.Errorf("insert common variant: %w", err)
		}
		affected, _ := res.RowsAffected()
		if affected > 0 {
			result.Promoted++
		} else {
			result.Skipped++
		}
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("iterate promotion candidates: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit glossary promotion: %w", err)
	}
	return result, nil
}

func upsertTerm(ctx context.Context, tx *sql.Tx, mediaKey string, entry glossary.GeneratedEntry) (int64, error) {
	normalized := strings.ToLower(strings.TrimSpace(entry.NormalizedTerm))
	if normalized == "" {
		return 0, fmt.Errorf("glossary term normalized_term is required")
	}
	display := strings.TrimSpace(entry.SourceTerm)
	if display == "" {
		display = normalized
	}

	var id int64
	err := tx.QueryRowContext(ctx, `
select id from glossary_terms
where scope = ? and media_key = ? and normalized_term = ?
limit 1`, glossary.ScopeMedia, mediaKey, normalized).Scan(&id)
	switch {
	case err == nil:
		_, err = tx.ExecContext(ctx, `
update glossary_terms
set display_term = ?, updated_at = current_timestamp, last_seen_at = current_timestamp
where id = ?`, display, id)
		if err != nil {
			return 0, fmt.Errorf("update glossary term: %w", err)
		}
		return id, nil
	case errors.Is(err, sql.ErrNoRows):
		res, err := tx.ExecContext(ctx, `
insert into glossary_terms(scope, media_key, normalized_term, display_term)
values (?, ?, ?, ?)`, glossary.ScopeMedia, mediaKey, normalized, display)
		if err != nil {
			return 0, fmt.Errorf("insert glossary term: %w", err)
		}
		id, err = res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("read glossary term id: %w", err)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("find glossary term: %w", err)
	}
}

func insertObservation(ctx context.Context, tx *sql.Tx, variantID int64, req glossary.UpsertRequest, entry glossary.GeneratedEntry) error {
	snippet := ""
	if len(entry.Evidence) > 0 {
		snippet = entry.Evidence[0]
	}
	subtitleHash := glossary.SubtitlePathHash(req.Job.SubtitlePath)
	_, err := tx.ExecContext(ctx, `
insert into glossary_observations(variant_id, job_id, media_key, subtitle_path_hash, season, episode, snippet, confidence)
values (?, ?, ?, ?, ?, ?, ?, ?)`,
		variantID,
		req.Job.JobID,
		req.MediaKey,
		subtitleHash,
		req.Job.Season,
		req.Job.Episode,
		snippet,
		entry.Confidence,
	)
	if err != nil {
		return fmt.Errorf("insert glossary observation: %w", err)
	}

	if req.Options.MaxObservationsPerVariant > 0 {
		_, err = tx.ExecContext(ctx, `
delete from glossary_observations
where variant_id = ?
  and id not in (
    select id
    from glossary_observations
    where variant_id = ?
    order by created_at desc, id desc
    limit ?
  )`, variantID, variantID, req.Options.MaxObservationsPerVariant)
		if err != nil {
			return fmt.Errorf("trim glossary observations: %w", err)
		}
	}
	return nil
}

func suppressExcessActiveVariants(ctx context.Context, tx *sql.Tx, termID int64, targetLanguage string, maxActive int) (int, error) {
	if maxActive <= 0 {
		return 0, nil
	}
	res, err := tx.ExecContext(ctx, `
update glossary_variants
set status = ?, updated_at = current_timestamp
where term_id = ?
  and target_language = ?
  and status = ?
  and id not in (
    select id
    from glossary_variants
    where term_id = ?
      and target_language = ?
      and status = ?
    order by confidence desc, evidence_count desc, last_seen_at desc, id desc
    limit ?
  )`,
		glossary.StatusSuppressed,
		termID,
		targetLanguage,
		glossary.StatusActive,
		termID,
		targetLanguage,
		glossary.StatusActive,
		maxActive,
	)
	if err != nil {
		return 0, fmt.Errorf("suppress glossary variants: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read suppressed glossary variants: %w", err)
	}
	return int(affected), nil
}

func parseSQLiteTime(value string) time.Time {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
