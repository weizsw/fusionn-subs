# Glossary Candidate Extraction Design

## Goal

Improve automatic glossary candidate extraction so brand names and other subtitle terminology are less likely to be missed before the glossary LLM sees them.

This change is limited to local candidate extraction. It does not change glossary storage, confidence thresholds, common-term promotion, translator argument building, or LLM response validation.

## Problem

The current extractor is too conservative for real subtitle terminology. It catches acronyms like `SO15` and multi-word Title Case phrases like `Counter Terrorism Command`, but it misses useful single-word proper nouns such as `Louboutins`. That prevents the glossary LLM from deciding whether the term should become a glossary entry.

At the same time, subtitles contain many capitalized sentence-start words. Expanding extraction without filtering would add noisy candidates like `Well`, `Look`, `Okay`, and `Thanks`.

## Assumptions

- Candidate extraction should favor moderate recall, because the glossary LLM can reject semantic noise.
- Injection should remain conservative through existing confidence thresholds.
- The first improvement should use deterministic heuristics and tests, not a brand database or a named-entity recognition dependency.
- `max_candidates` and `max_snippets_per_candidate` continue to bound prompt size.

## Design

Candidate extraction will support three additional term shapes:

1. Repeated single-word proper nouns.
   - Match single Title Case words when they appear more than once in the subtitle.
   - This catches recurring names and brands while avoiding most sentence-start noise.

2. Narrow brand-like singletons.
   - Allow a single occurrence when the token shape is strongly brand/product-like.
   - Initial heuristics:
     - mixed-case product tokens such as `iPhone`;
     - Title Case plural tokens ending in `s`, with length greater than 4, after stoplist filtering, such as `Louboutins`.

3. Internal punctuation and digit terms.
   - Allow useful internal apostrophes, hyphens, and digits.
   - Examples: `Spider-Man`, `O'Neill`, `G-Force`, `MI6`, `iPhone`.

The extractor will first count all candidate-shaped occurrences. It will then apply single-token inclusion rules after aggregate frequency is known. Before returning candidates, it will apply a small stoplist for common subtitle discourse words, especially noisy single-word Title Case matches. Examples include `I`, `You`, `The`, `Okay`, `Yeah`, `Well`, `Look`, and `Thanks`.

## Data Flow

The existing flow remains unchanged:

1. `ExtractCandidates` parses subtitles with `go-astisub`.
2. It scans cue text for candidate term occurrences.
3. It normalizes terms, aggregates frequency, and collects bounded snippets.
4. It sorts by frequency and normalized term.
5. It applies `max_candidates`.
6. The glossary service sends extracted candidates to the glossary LLM.

Only step 2 and local filtering rules change.

## Error Handling

No new error cases are introduced. Parsing, file-size limits, cue-count limits, and glossary failure behavior remain unchanged.

If a candidate shape is ambiguous, the extractor should prefer dropping obvious stoplist noise and leaving semantic judgment to the LLM for the remaining candidates.

## Testing

Add extractor tests for:

- `Louboutins` is extracted as a likely brand term.
- Repeated single Title Case terms are extracted.
- Common sentence-start noise is filtered.
- Hyphenated, apostrophe, and digit-containing terms are extracted.
- Existing acronym and multi-word phrase behavior still works.

Run:

```bash
go test ./internal/service/glossary -run ExtractCandidates -v
```

If implementation touches shared glossary behavior unexpectedly, broaden verification to:

```bash
go test ./internal/service/glossary ./internal/service/worker ./internal/service/translator
```
