# Glossary Candidate Selection Design

## Goal

Improve automatic glossary selection so terms that are likely to cause inconsistent subtitle translation reach the glossary LLM, while common terms with stable obvious translations are unlikely to be stored or injected.

The pain point is not that every proper noun needs a glossary entry. The pain point is repeated terminology being translated inconsistently across sentences or episodes, or being translated in one sentence and preserved in another. The feature should prioritize terms where glossary guidance reduces that risk.

## Problem

The current extractor is too conservative for real subtitle terminology. It catches acronyms like `SO15` and multi-word Title Case phrases like `Counter Terrorism Command`, but it misses useful single-word proper nouns such as `Louboutins`. That prevents the glossary LLM from deciding whether the term should become a glossary entry.

The opposite problem is also real. Multi-word proper nouns such as `New York` and `Madison Avenue` are easy to extract, but they are not always valuable glossary entries. `New York` has a standard translation that most translation models handle consistently. `Madison Avenue` may refer to a street, an advertising industry metonym, or a media-specific concept. Forcing one translation through `--terminology` can make those cases worse.

The current phrase regex can also create malformed candidates by joining a sentence-start ordinary word with a proper noun. For example, `Have Carbone cater.` can become `Have Carbone`. That candidate is wrong. The useful candidate is `Carbone`, if anything, because it is a restaurant or brand-like proper noun in context.

Subtitle syntax adds another source of noise. ASS/SSA subtitles can contain bilingual lines, line breaks, speaker labels, style overrides, and caption text such as `(laughs)` or `[door opens]`. The extractor should use parsed subtitle lines as text boundaries and avoid producing glossary candidates from formatting, generic speaker labels, or caption-only descriptions.

## Assumptions

- Candidate extraction should favor moderate recall, because the glossary LLM can reject semantic noise.
- LLM glossary generation should be selective: return only terms that are likely to benefit from consistency guidance.
- Fixed terminology injection should remain conservative through existing confidence thresholds and should not inject context-dependent entries.
- The first improvement should use deterministic heuristics and prompt rules, not a brand database or a named-entity recognition dependency.
- `max_candidates` and `max_snippets_per_candidate` continue to bound prompt size.

## Scope

Change these components:

- `internal/service/glossary/extractor.go`
- `internal/service/glossary/llm_openai.go`
- `internal/service/glossary/prompt_builder.go`
- glossary tests for the affected behavior

Do not change:

- SQLite schema
- glossary promotion policy
- worker orchestration
- translator argument format
- configured confidence thresholds

Adding category constants is allowed because `category` is stored as text and does not require a migration.

## Design

Use a three-gate strategy.

### Gate 1: Candidate Extraction

Candidate extraction remains recall-oriented but bounded. It will scan parsed subtitle lines rather than `item.String()` for the whole cue, so candidates do not cross ASS/SSA `\N` line boundaries or astisub's `" - "` joiner.

Before matching candidate shapes, each line should be normalized for extraction:

- Use the text produced by `go-astisub` line items so ASS/SSA style effects such as `{\rDefault_1}` are not treated as candidate text.
- Keep bilingual cues safe by ignoring CJK and other non-Latin-script spans for candidate matching while preserving the original line as evidence snippet context.
- Strip common speaker prefixes such as `MAN:`, `WOMAN:`, `ANNOUNCER:`, `TV:`, `RADIO:`, `BOTH:`, and `ALL:` before candidate matching.
- Do not extract candidates from caption-only lines wrapped in parentheses or square brackets when they look like sound/action descriptions, such as `(laughs)` or `[Door Opens]`.

Candidate extraction will support these term shapes:

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
   - Examples: `Spider-Man`, `O'Neill`, `G-Force`, `MI6`, `iPhone`, `Studio 54`, `Area 51`.

4. Dotted, ampersand, and slash acronyms.
   - Allow compact organization or unit terms such as `U.N.`, `S.H.I.E.L.D.`, `AT&T`, `R&D`, and `S.W.A.T.`.
   - Filter common discourse acronyms such as `OK` unless snippets show a special media-specific meaning.

5. Proper phrases with internal connectors.
   - Allow lowercase connectors inside a proper phrase when both sides are anchored by proper tokens.
   - Examples: `Bank of America`, `Lord of the Rings`, `University of Oxford`, `House of Commons`.
   - Do not allow connectors at the beginning or end of a candidate.

6. Titles, ranks, and acronym-plus-name phrases.
   - Allow terms such as `Dr. House`, `Mr. Robot`, `St. Mary's`, `DCI Carey`, and `Agent Carter`.
   - Keep these bounded to short phrases so ordinary dialogue does not become glossary material.

7. Unicode Latin names and brands.
   - Allow Latin-script names with accents or curly apostrophes, such as `Hermès`, `Pokémon`, `Beyoncé`, and `D'Angelo`.
   - Lowercase-only brands such as `adidas` are out of scope for deterministic extraction without a brand dictionary.

The extractor will first count all candidate-shaped occurrences. It will then apply single-token inclusion rules after aggregate frequency is known. Before returning candidates, it will apply a small stoplist for common subtitle discourse words, especially noisy single-word Title Case matches. Examples include `I`, `You`, `The`, `Okay`, `Yeah`, `Well`, `Look`, and `Thanks`.

Multi-word phrase extraction must not join ordinary sentence-start words with a following proper noun. If the first word of a two-word candidate is a phrase-leading verb or discourse word such as `Have`, `How`, `What`, `Let`, `Tell`, `Ask`, `Call`, `Meet`, `Look`, or `Well`, drop the phrase. A trailing proper noun inside that rejected phrase may still be considered as a single-token candidate when it is not the sentence-start token. This turns `Have Carbone` into `Carbone` rather than keeping the malformed phrase.

Do not treat `The` as a universal phrase-leading stop word. Phrases such as `The Hague`, `The Citadel`, `The Company`, and `The Matrix` may be meaningful; the LLM selection gate should decide whether they are worth storing.

Possessive variants should normalize to the base term for matching where practical. For example, `Carbone's` and `Carbone` should share the normalized term `carbone`, and `O'Neill's` should share `o'neill`. The display/source term used for glossary entries should prefer the non-possessive base form.

Candidate sorting should favor higher-risk terms before applying `max_candidates`:

1. acronyms and acronym-digit terms such as `SO15`, `MI6`, `S.H.I.E.L.D.`;
2. brand/product-like terms such as `Louboutins`, `iPhone`, `AT&T`;
3. punctuation, connector, or digit terms such as `Spider-Man`, `G-Force`, `Bank of America`, `Studio 54`;
4. titles, ranks, and acronym-plus-name terms such as `Dr. House`, `DCI Carey`;
5. repeated or non-sentence-initial single proper nouns;
6. ordinary multi-word Title Case phrases.

Frequency remains the tie-breaker inside each priority group.

### Gate 2: LLM Glossary Selection

The glossary LLM prompt must make selection narrower than extraction. It should instruct the model to return entries only for candidates likely to cause consistency problems:

- acronyms and organization names;
- brands and product names;
- fictional or media-specific places, groups, abilities, artifacts, titles, and technical terms;
- names or phrases whose snippets suggest special in-media meaning;
- terms whose translation mode should be consistently `preserve`, `transliterate`, or a fixed translation.

The prompt must also instruct the model to skip candidates that do not need fixed glossary guidance:

- common real-world places with standard translations, such as `New York`, unless snippets show special media-specific meaning;
- ordinary street names or addresses, such as `Madison Avenue`, unless used as a recurring concept or organization;
- malformed phrases that include ordinary leading verbs or discourse words, such as `Have Carbone`; return the meaningful proper noun instead, such as `Carbone`, only if it needs consistency guidance;
- common English words only capitalized because they start a sentence;
- generic speaker labels and caption descriptions, such as `MAN`, `WOMAN`, `Door Opens`, and `Phone Ringing`;
- common abbreviations with stable obvious translations, such as `OK` or `TV`, unless they are media-specific;
- phrases whose target translation should vary by sentence.

Add explicit categories for brand/product terms:

- `brand`
- `product`

The existing JSON response shape remains the transport contract. The category list in the prompt expands, but no database migration is required.

### Gate 3: Fixed Terminology Injection

`--terminology SOURCE::TRANSLATION` is a fixed mapping. The prompt builder should not inject generated entries with `translation_mode:"contextual"` because those entries require sentence-level judgment.

Curated entries may still be injected according to existing confidence behavior because curated data is operator-controlled.

This gate prevents the glossary from forcing a single translation for context-dependent candidates even if an LLM generated them.

## Data Flow

1. `ExtractCandidates` parses subtitles with `go-astisub`.
2. It scans cue text for candidate term occurrences.
3. It normalizes terms, aggregates frequency, and collects bounded snippets.
4. It filters obvious local noise and sorts candidates by glossary-risk priority, frequency, then normalized term.
5. It applies `max_candidates`.
6. The glossary service sends candidates to the glossary LLM.
7. The glossary LLM returns only terms worth storing for consistency guidance.
8. Storage keeps generated entries using existing schema and confidence rules.
9. `BuildTerminology` injects eligible fixed mappings and skips contextual generated entries.

## Error Handling

No new runtime error cases are introduced. Parsing, file-size limits, cue-count limits, LLM failures, and glossary failure behavior remain unchanged.

If a candidate shape is ambiguous, the extractor should keep bounded possible terminology and leave semantic judgment to the LLM. If an LLM marks an entry contextual, the prompt builder should avoid fixed injection unless the entry is curated.

## Testing

Add or update tests for:

- `Louboutins` is extracted as a likely brand term.
- Repeated single Title Case terms are extracted.
- Common sentence-start noise is filtered.
- `Have Carbone` is not extracted as a phrase, while `Carbone` can be extracted as the meaningful candidate.
- ASS/SSA bilingual lines and style overrides do not create candidates from style names or Chinese text.
- Common speaker labels and caption-only descriptions are filtered.
- Hyphenated, apostrophe, and digit-containing terms are extracted.
- Dotted, ampersand, and slash acronyms are extracted.
- Proper phrases with internal connectors are extracted.
- Titles, ranks, and acronym-plus-name phrases are extracted.
- Possessive variants normalize to the same base term.
- Unicode Latin names and brands are extracted.
- Higher-risk candidates sort before ordinary multi-word place phrases when `max_candidates` is constrained.
- The LLM prompt tells the model to skip common real-world places such as `New York` unless they have special media-specific meaning.
- The LLM prompt includes `brand` and `product` categories.
- `BuildTerminology` skips generated contextual entries.
- `BuildTerminology` still allows curated contextual entries.
- Existing acronym and multi-word phrase behavior still works.

Run focused tests:

```bash
go test ./internal/service/glossary -run 'ExtractCandidates|GlossaryPrompt|BuildTerminology' -v
```

Then run related service tests:

```bash
go test ./internal/service/glossary ./internal/service/worker ./internal/service/translator
```
