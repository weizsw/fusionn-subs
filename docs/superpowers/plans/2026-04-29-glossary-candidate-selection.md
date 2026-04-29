# Glossary Candidate Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve glossary candidate selection so high-risk terminology reaches the glossary LLM, common/stable proper nouns are less likely to be stored, and contextual entries are not injected as fixed terminology.

**Architecture:** Use three gates. The extractor broadens and ranks candidate shapes while filtering subtitle syntax noise. The glossary LLM prompt narrows generated entries to consistency-risk terms. The prompt builder skips generated contextual entries before producing `--terminology` pairs.

**Tech Stack:** Go 1.23, `regexp`, `sort`, `strings`, `unicode`, `github.com/asticode/go-astisub`, standard `testing`.

**Spec:** `docs/superpowers/specs/2026-04-29-glossary-candidate-extraction-design.md`

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/service/glossary/extractor_test.go` | Modify | Add focused tests for subtitle syntax cleanup, malformed phrase filtering, expanded candidate shapes, normalization, and priority sorting. |
| `internal/service/glossary/extractor.go` | Modify | Scan parsed subtitle lines, normalize candidate text, extract broader term shapes, filter local noise, rank candidates before `max_candidates`. |
| `internal/service/glossary/types.go` | Modify | Add `brand` and `product` category constants. |
| `internal/service/glossary/llm_openai_test.go` | Modify | Assert prompt schema includes selection/skip rules and new categories. |
| `internal/service/glossary/llm_openai.go` | Modify | Tighten glossary system prompt with selective generation rules. |
| `internal/service/glossary/prompt_builder_test.go` | Modify | Add fixed-terminology eligibility tests for contextual generated vs curated entries. |
| `internal/service/glossary/prompt_builder.go` | Modify | Skip generated `contextual` entries when building fixed terminology pairs. |

---

### Task 1: Add Extractor Edge-Case Coverage

**Files:**
- Modify: `internal/service/glossary/extractor_test.go`

- [ ] **Step 1: Add extractor test helper**

Append this helper to `internal/service/glossary/extractor_test.go`:

```go
func extractCandidateMap(t *testing.T, filename, content string, opts ExtractOptions) map[string]Candidate {
	t.Helper()

	path := filepath.Join(t.TempDir(), filename)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if opts.MaxSubtitleBytes == 0 {
		opts.MaxSubtitleBytes = 1 << 20
	}
	if opts.MaxCues == 0 {
		opts.MaxCues = 100
	}
	if opts.MaxCandidates == 0 {
		opts.MaxCandidates = 50
	}
	if opts.MaxSnippetsPerCandidate == 0 {
		opts.MaxSnippetsPerCandidate = 2
	}

	candidates, err := ExtractCandidates(path, opts)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	found := map[string]Candidate{}
	for _, candidate := range candidates {
		found[candidate.NormalizedTerm] = candidate
	}
	return found
}
```

- [ ] **Step 2: Add tests for brand-like and repeated single terms**

Append:

```go
func TestExtractCandidatesIncludesBrandLikeSingletons(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
She wore Louboutins to the gala.

2
00:00:04,000 --> 00:00:06,000
I checked the address on my iPhone.
`, ExtractOptions{})

	for _, term := range []string{"louboutins", "iphone"} {
		if _, ok := found[term]; !ok {
			t.Fatalf("expected %q candidate, got %#v", term, found)
		}
	}
}

func TestExtractCandidatesIncludesRepeatedSingleProperNouns(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
Carey reviewed the feed.

2
00:00:04,000 --> 00:00:06,000
Later, Carey called back.
`, ExtractOptions{})

	if found["carey"].Frequency != 2 {
		t.Fatalf("Carey frequency = %d, want 2; candidates = %#v", found["carey"].Frequency, found)
	}
}
```

- [ ] **Step 3: Add tests for malformed phrases and local noise**

Append:

```go
func TestExtractCandidatesFiltersMalformedPhraseAndKeepsMeaningfulName(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
Have Carbone cater.
`, ExtractOptions{})

	if _, ok := found["have carbone"]; ok {
		t.Fatalf("did not expect malformed phrase, got %#v", found)
	}
	if _, ok := found["carbone"]; !ok {
		t.Fatalf("expected Carbone candidate, got %#v", found)
	}
}

func TestExtractCandidatesFiltersCommonSingleWordNoise(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
Well, look, okay.

2
00:00:04,000 --> 00:00:06,000
Thanks. Yeah. You know.

3
00:00:07,000 --> 00:00:09,000
The house was quiet.
`, ExtractOptions{})

	for _, term := range []string{"well", "look", "okay", "thanks", "yeah", "you", "the"} {
		if _, ok := found[term]; ok {
			t.Fatalf("did not expect noisy %q candidate, got %#v", term, found)
		}
	}
}
```

- [ ] **Step 4: Add tests for subtitle syntax cleanup**

Append:

```go
func TestExtractCandidatesHandlesASSBilingualLinesAndStyleOverrides(t *testing.T) {
	found := extractCandidateMap(t, "episode.ass", `[Script Info]
ScriptType: v4.00+

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,10,1
Style: Default_1,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:08:49.61,0:08:53.24,Default,,0,0,0,,让 Carbone 承办餐饮\N{\rDefault_1}Have Carbone cater.
`, ExtractOptions{})

	if _, ok := found["default"]; ok {
		t.Fatalf("did not expect style name candidate, got %#v", found)
	}
	if _, ok := found["have carbone"]; ok {
		t.Fatalf("did not expect malformed phrase from ASS line, got %#v", found)
	}
	if _, ok := found["carbone"]; !ok {
		t.Fatalf("expected Carbone candidate, got %#v", found)
	}
}

func TestExtractCandidatesFiltersSpeakerLabelsAndCaptionOnlyLines(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
MAN: We need SO15.

2
00:00:04,000 --> 00:00:06,000
[Door Opens]

3
00:00:07,000 --> 00:00:09,000
(Phone Ringing)
`, ExtractOptions{})

	for _, term := range []string{"man", "door opens", "phone ringing"} {
		if _, ok := found[term]; ok {
			t.Fatalf("did not expect %q candidate, got %#v", term, found)
		}
	}
	if _, ok := found["so15"]; !ok {
		t.Fatalf("expected SO15 candidate, got %#v", found)
	}
}
```

- [ ] **Step 5: Add tests for expanded shapes, normalization, and priority**

Append:

```go
func TestExtractCandidatesIncludesExpandedTermShapes(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
Spider-Man called O'Neill from MI6.

2
00:00:04,000 --> 00:00:06,000
The G-Force reading near Studio 54 worried S.H.I.E.L.D.

3
00:00:07,000 --> 00:00:09,000
AT&T sent R&D notes to Bank of America.

4
00:00:10,000 --> 00:00:12,000
Dr. House mentioned DCI Carey and Hermès.
`, ExtractOptions{})

	for _, term := range []string{
		"spider-man",
		"o'neill",
		"mi6",
		"g-force",
		"studio 54",
		"s.h.i.e.l.d.",
		"at&t",
		"r&d",
		"bank of america",
		"dr. house",
		"dci carey",
		"hermès",
	} {
		if _, ok := found[term]; !ok {
			t.Fatalf("expected %q candidate, got %#v", term, found)
		}
	}
}

func TestExtractCandidatesNormalizesPossessiveVariants(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
Carbone's private room was full.

2
00:00:04,000 --> 00:00:06,000
I called Carbone again.

3
00:00:07,000 --> 00:00:09,000
O'Neill's team found O'Neill.
`, ExtractOptions{})

	if found["carbone"].Frequency != 2 {
		t.Fatalf("Carbone frequency = %d, want 2; candidates = %#v", found["carbone"].Frequency, found)
	}
	if found["o'neill"].Frequency != 2 {
		t.Fatalf("O'Neill frequency = %d, want 2; candidates = %#v", found["o'neill"].Frequency, found)
	}
}

func TestExtractCandidatesPrioritizesHighRiskTermsBeforeCommonPhrases(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
New York and Madison Avenue were quiet.

2
00:00:04,000 --> 00:00:06,000
SO15 tracked Louboutins near Spider-Man.
`, ExtractOptions{MaxCandidates: 3})

	for _, term := range []string{"so15", "louboutins", "spider-man"} {
		if _, ok := found[term]; !ok {
			t.Fatalf("expected prioritized %q candidate, got %#v", term, found)
		}
	}
	for _, term := range []string{"new york", "madison avenue"} {
		if _, ok := found[term]; ok {
			t.Fatalf("did not expect lower-priority %q candidate with max cap, got %#v", term, found)
		}
	}
}
```

- [ ] **Step 6: Run focused extractor tests and verify failure**

Run:

```bash
go test ./internal/service/glossary -run ExtractCandidates -v
```

Expected: FAIL. The current extractor should miss several new shapes and still emit malformed/noisy candidates.

---

### Task 2: Implement Broader, Bounded Extractor

**Files:**
- Modify: `internal/service/glossary/extractor.go`

- [ ] **Step 1: Replace extractor imports and pattern globals**

In `internal/service/glossary/extractor.go`, replace the import block with:

```go
import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"

	astisub "github.com/asticode/go-astisub"
)
```

Replace `candidatePattern` with:

```go
const (
	priorityAcronym = 600
	priorityBrand   = 500
	prioritySpecial = 400
	priorityTitle   = 300
	prioritySingle  = 200
	priorityPhrase  = 100
)

type candidatePatternSpec struct {
	pattern  *regexp.Regexp
	priority int
}

type candidateMatch struct {
	term      string
	start     int
	end       int
	priority  int
	initial   bool
	multiWord bool
}

type candidateAccumulator struct {
	candidate     Candidate
	priority      int
	hasNonInitial bool
}

var (
	titleTokenPattern = `[\p{Lu}][\p{L}\p{M}0-9]*(?:[-'’][\p{L}\p{M}0-9]+)*`
	mixedCasePattern = `[\p{Ll}]+[\p{Lu}][\p{L}\p{M}0-9]*`
	connectorPattern = `(?:of|the|for|de|del|la|le|van|von|du)`

	candidatePatterns = []candidatePatternSpec{
		{regexp.MustCompile(`[A-Z]\.(?:[A-Z]\.)+`), priorityAcronym},
		{regexp.MustCompile(`\b[A-Z]+(?:[&/][A-Z]+)+\b`), priorityBrand},
		{regexp.MustCompile(`\b[A-Z]{2,}\d*\b`), priorityAcronym},
		{regexp.MustCompile(`\b(?:Dr|Mr|Mrs|Ms|St)\.\s+` + titleTokenPattern + `\b`), priorityTitle},
		{regexp.MustCompile(`\b[A-Z]{2,}\d*\s+` + titleTokenPattern + `\b`), priorityTitle},
		{regexp.MustCompile(`\b(?:Agent|Captain|Detective|Doctor|Officer|President|Professor)\s+` + titleTokenPattern + `\b`), priorityTitle},
		{regexp.MustCompile(`\b` + titleTokenPattern + `\s+\d+\b`), prioritySpecial},
		{regexp.MustCompile(`\b` + titleTokenPattern + `(?:\s+` + titleTokenPattern + `)*\s+` + connectorPattern + `(?:\s+` + connectorPattern + `)*\s+` + titleTokenPattern + `(?:\s+(?:` + connectorPattern + `|` + titleTokenPattern + `)){0,3}\b`), prioritySpecial},
		{regexp.MustCompile(`\b` + titleTokenPattern + `(?:\s+` + titleTokenPattern + `){1,3}\b`), priorityPhrase},
		{regexp.MustCompile(`\b(?:` + titleTokenPattern + `|` + mixedCasePattern + `)\b`), prioritySingle},
	}

	noisySingleTermStoplist = map[string]struct{}{
		"a": {}, "all": {}, "an": {}, "and": {}, "both": {}, "but": {}, "hey": {}, "i": {},
		"look": {}, "man": {}, "no": {}, "okay": {}, "ok": {}, "or": {}, "please": {},
		"sorry": {}, "thanks": {}, "thank": {}, "the": {}, "tv": {}, "well": {}, "woman": {},
		"yeah": {}, "yes": {}, "you": {},
	}

	phraseLeadingStoplist = map[string]struct{}{
		"ask": {}, "call": {}, "do": {}, "does": {}, "did": {}, "get": {}, "go": {}, "have": {},
		"how": {}, "let": {}, "look": {}, "meet": {}, "see": {}, "take": {}, "tell": {},
		"thank": {}, "thanks": {}, "well": {}, "what": {}, "where": {}, "who": {}, "why": {},
	}

	speakerPrefixPattern = regexp.MustCompile(`^\s*([A-Z][A-Z0-9 .'-]{0,24}):\s*`)
)
```

- [ ] **Step 2: Replace the extraction aggregation block**

Inside `ExtractCandidates`, replace the current block that starts with `byTerm := make(map[string]*Candidate)` and ends with `return out, nil` with:

```go
	byTerm := make(map[string]*candidateAccumulator)
	for _, item := range subs.Items {
		for _, line := range item.Lines {
			snippet := strings.TrimSpace(line.String())
			if snippet == "" || isCaptionOnlyLine(snippet) {
				continue
			}
			matchText := prepareCandidateLine(snippet)
			if strings.TrimSpace(matchText) == "" {
				continue
			}
			for _, match := range collectCandidateMatches(matchText) {
				normalized := normalizeTerm(match.term)
				if normalized == "" {
					continue
				}
				acc := byTerm[normalized]
				if acc == nil {
					acc = &candidateAccumulator{
						candidate: Candidate{
							Term:           displayTermForCandidate(match.term),
							NormalizedTerm: normalized,
						},
						priority: match.priority,
					}
					byTerm[normalized] = acc
				}
				if match.priority > acc.priority {
					acc.priority = match.priority
				}
				if !match.initial {
					acc.hasNonInitial = true
				}
				acc.candidate.Frequency++
				if opts.MaxSnippetsPerCandidate <= 0 || len(acc.candidate.Snippets) < opts.MaxSnippetsPerCandidate {
					acc.candidate.Snippets = append(acc.candidate.Snippets, snippet)
				}
			}
		}
	}

	out := make([]candidateAccumulator, 0, len(byTerm))
	for _, acc := range byTerm {
		if shouldKeepCandidate(*acc) {
			out = append(out, *acc)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].priority == out[j].priority {
			if out[i].candidate.Frequency == out[j].candidate.Frequency {
				return out[i].candidate.NormalizedTerm < out[j].candidate.NormalizedTerm
			}
			return out[i].candidate.Frequency > out[j].candidate.Frequency
		}
		return out[i].priority > out[j].priority
	})
	if opts.MaxCandidates > 0 && len(out) > opts.MaxCandidates {
		out = out[:opts.MaxCandidates]
	}

	candidates := make([]Candidate, 0, len(out))
	for _, acc := range out {
		candidates = append(candidates, acc.candidate)
	}
	return candidates, nil
```

- [ ] **Step 3: Add extractor helper functions**

Below `ExtractCandidates`, add:

```go
func prepareCandidateLine(text string) string {
	text = stripSpeakerPrefix(strings.TrimSpace(text))
	var b strings.Builder
	lastWasSpace := false
	for _, r := range text {
		keep := unicode.In(r, unicode.Latin) || unicode.IsDigit(r) || unicode.IsSpace(r) || strings.ContainsRune(" .'-’&/", r)
		if !keep {
			if !lastWasSpace {
				b.WriteByte(' ')
				lastWasSpace = true
			}
			continue
		}
		if unicode.IsSpace(r) {
			if !lastWasSpace {
				b.WriteByte(' ')
				lastWasSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastWasSpace = false
	}
	return strings.TrimSpace(b.String())
}

func stripSpeakerPrefix(text string) string {
	match := speakerPrefixPattern.FindStringSubmatchIndex(text)
	if match == nil {
		return text
	}
	label := strings.ToLower(strings.TrimSpace(text[match[2]:match[3]]))
	label = strings.ReplaceAll(label, ".", "")
	if _, ok := noisySingleTermStoplist[label]; ok {
		return strings.TrimSpace(text[match[1]:])
	}
	switch label {
	case "announcer", "both", "man", "radio", "tv", "woman":
		return strings.TrimSpace(text[match[1]:])
	default:
		return text
	}
}

func isCaptionOnlyLine(text string) bool {
	text = strings.TrimSpace(text)
	if len(text) < 2 {
		return false
	}
	open, close := text[0], text[len(text)-1]
	if !((open == '(' && close == ')') || (open == '[' && close == ']')) {
		return false
	}
	inner := strings.TrimSpace(text[1 : len(text)-1])
	if inner == "" {
		return true
	}
	return !regexp.MustCompile(`[A-Z]{2,}\d*|[A-Z]\.(?:[A-Z]\.)+|[a-z]+[A-Z]`).MatchString(inner)
}

func collectCandidateMatches(text string) []candidateMatch {
	var matches []candidateMatch
	for _, spec := range candidatePatterns {
		for _, loc := range spec.pattern.FindAllStringIndex(text, -1) {
			term := strings.TrimSpace(text[loc[0]:loc[1]])
			if term == "" || isMalformedPhrase(term) || hasDanglingConnector(term) {
				continue
			}
			matches = append(matches, candidateMatch{
				term:      term,
				start:     loc[0],
				end:       loc[1],
				priority:  refinePriority(term, spec.priority),
				initial:   isSentenceInitial(text, loc[0]),
				multiWord: strings.Contains(term, " "),
			})
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].start == matches[j].start {
			if matches[i].end == matches[j].end {
				return matches[i].priority > matches[j].priority
			}
			return matches[i].end-matches[i].start > matches[j].end-matches[j].start
		}
		return matches[i].start < matches[j].start
	})

	var kept []candidateMatch
	var covered []candidateMatch
	for _, match := range matches {
		if isDuplicateSpan(match, kept) {
			continue
		}
		if !match.multiWord && isCoveredByMultiWord(match, covered) {
			continue
		}
		kept = append(kept, match)
		if match.multiWord {
			covered = append(covered, match)
		}
	}
	return kept
}
```

- [ ] **Step 4: Add candidate filtering, priority, and normalization helpers**

Below the helper functions from step 3, add:

```go
func shouldKeepCandidate(acc candidateAccumulator) bool {
	term := strings.TrimSpace(acc.candidate.Term)
	normalized := strings.TrimSpace(acc.candidate.NormalizedTerm)
	if term == "" || normalized == "" {
		return false
	}
	if _, ok := noisySingleTermStoplist[normalized]; ok {
		return false
	}
	if strings.Contains(term, " ") {
		return true
	}
	if acc.priority >= priorityAcronym || acc.priority >= priorityBrand {
		return true
	}
	if acc.candidate.Frequency > 1 || acc.hasNonInitial {
		return true
	}
	return isBrandLikeSingleton(term) || hasInternalPunctuationOrDigit(term)
}

func refinePriority(term string, fallback int) int {
	switch {
	case isAcronymLike(term):
		return priorityAcronym
	case strings.ContainsAny(term, "&/") || isBrandLikeSingleton(term):
		return priorityBrand
	case strings.ContainsAny(term, "-'’.") || hasInternalPunctuationOrDigit(term) || hasConnector(term):
		return prioritySpecial
	case strings.Contains(term, " ") && fallback == priorityPhrase:
		return priorityPhrase
	default:
		return fallback
	}
}

func isMalformedPhrase(term string) bool {
	words := strings.Fields(term)
	if len(words) != 2 {
		return false
	}
	first := strings.ToLower(strings.Trim(words[0], " .'’"))
	_, ok := phraseLeadingStoplist[first]
	return ok
}

func hasDanglingConnector(term string) bool {
	words := strings.Fields(term)
	if len(words) == 0 {
		return false
	}
	_, first := connectorWord(words[0])
	_, last := connectorWord(words[len(words)-1])
	return first || last
}

func hasConnector(term string) bool {
	for _, word := range strings.Fields(term) {
		if _, ok := connectorWord(word); ok {
			return true
		}
	}
	return false
}

func connectorWord(word string) (string, bool) {
	word = strings.ToLower(strings.Trim(word, " .'’"))
	switch word {
	case "de", "del", "du", "for", "la", "le", "of", "the", "van", "von":
		return word, true
	default:
		return word, false
	}
}

func isDuplicateSpan(match candidateMatch, kept []candidateMatch) bool {
	for _, existing := range kept {
		if existing.start == match.start && existing.end == match.end {
			return true
		}
	}
	return false
}

func isCoveredByMultiWord(match candidateMatch, covered []candidateMatch) bool {
	for _, existing := range covered {
		if match.start >= existing.start && match.end <= existing.end {
			return true
		}
	}
	return false
}

func isSentenceInitial(text string, start int) bool {
	prefix := strings.TrimSpace(text[:start])
	if prefix == "" {
		return true
	}
	for i := len(prefix) - 1; i >= 0; i-- {
		switch prefix[i] {
		case '.', '?', '!', ':', ';':
			return true
		case ' ', '\t':
			continue
		default:
			return false
		}
	}
	return true
}

func isAcronymLike(term string) bool {
	letters := 0
	for _, r := range term {
		switch {
		case r >= 'A' && r <= 'Z':
			letters++
		case unicode.IsDigit(r) || strings.ContainsRune(".&/", r):
			continue
		default:
			return false
		}
	}
	return letters >= 2
}

func isBrandLikeSingleton(term string) bool {
	if regexp.MustCompile(`^[\p{Ll}]+[\p{Lu}][\p{L}\p{M}0-9]*$`).MatchString(term) {
		return true
	}
	return regexp.MustCompile(`^[\p{Lu}][\p{Ll}\p{M}]{3,}s$`).MatchString(term)
}

func hasInternalPunctuationOrDigit(term string) bool {
	for _, r := range term {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return strings.ContainsAny(term, "-'’.&/")
}

func displayTermForCandidate(term string) string {
	term = strings.TrimSpace(term)
	term = strings.TrimSuffix(term, "'s")
	term = strings.TrimSuffix(term, "'S")
	term = strings.TrimSuffix(term, "’s")
	term = strings.TrimSuffix(term, "’S")
	return strings.TrimSpace(term)
}
```

- [ ] **Step 5: Replace `normalizeTerm`**

Replace `normalizeTerm` with:

```go
func normalizeTerm(term string) string {
	term = displayTermForCandidate(term)
	term = strings.ToLower(strings.TrimSpace(term))
	term = strings.Join(strings.Fields(term), " ")
	return term
}
```

- [ ] **Step 6: Format and run focused extractor tests**

Run:

```bash
gofmt -w internal/service/glossary/extractor.go internal/service/glossary/extractor_test.go
go test ./internal/service/glossary -run ExtractCandidates -v
```

Expected: PASS for all extractor tests.

- [ ] **Step 7: Commit extractor changes**

Run:

```bash
git add internal/service/glossary/extractor.go internal/service/glossary/extractor_test.go
git commit -m "feat: improve glossary candidate extraction"
```

Expected: commit succeeds with only extractor files staged.

---

### Task 3: Tighten LLM Glossary Selection Prompt

**Files:**
- Modify: `internal/service/glossary/types.go`
- Modify: `internal/service/glossary/llm_openai_test.go`
- Modify: `internal/service/glossary/llm_openai.go`

- [ ] **Step 1: Add prompt selection tests**

Append to `internal/service/glossary/llm_openai_test.go`:

```go
func TestGlossarySystemPromptDescribesSelectiveGeneration(t *testing.T) {
	for _, want := range []string{
		"brand|product",
		"Return entries only for candidates likely to cause consistency problems",
		"Skip common real-world places with standard translations, such as New York",
		"Skip ordinary street names or addresses, such as Madison Avenue",
		"Skip malformed phrases such as Have Carbone",
		"Skip generic speaker labels and caption descriptions",
		"Skip common abbreviations with stable obvious translations, such as OK or TV",
	} {
		if !strings.Contains(glossarySystemPrompt, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, glossarySystemPrompt)
		}
	}
}
```

- [ ] **Step 2: Run prompt tests and verify failure**

Run:

```bash
go test ./internal/service/glossary -run GlossaryPrompt -v
```

Expected: FAIL because the current prompt does not include the new selection and skip rules.

- [ ] **Step 3: Add category constants**

In `internal/service/glossary/types.go`, after `CategoryAcronym`, add:

```go
	CategoryBrand         Category = "brand"
	CategoryProduct       Category = "product"
```

- [ ] **Step 4: Replace the glossary system prompt**

In `internal/service/glossary/llm_openai.go`, replace `glossarySystemPrompt` with:

```go
	glossarySystemPrompt = `You extract subtitle glossary entries from candidates. Return strict JSON only with this shape:
{"entries":[{"source_term":"SOURCE_FROM_CANDIDATES","normalized_term":"NORMALIZED_FROM_CANDIDATES","target_language":"zh-Hans","target_text":"TARGET_TEXT","definition":"SHORT_DEFINITION","translation_mode":"translate|preserve|transliterate|contextual","category":"acronym|brand|product|organization|character|place|technical_term|phrase","confidence":0.0,"evidence":["SNIPPET_FROM_CANDIDATE"]}]}
Rules:
- source_term must copy a candidates[].source_term exactly.
- normalized_term must copy the matching candidates[].normalized_term exactly.
- target_text must be non-empty.
- Return entries only for candidates likely to cause consistency problems across subtitle lines or episodes.
- Prefer acronyms, organization names, brands, product names, fictional/media-specific places, groups, abilities, artifacts, titles, and technical terms.
- Prefer terms that should be consistently preserved, transliterated, or translated with one fixed rendering.
- Use translation_mode "contextual" only when the term is worth remembering but should not be injected as one fixed SOURCE::TRANSLATION mapping.
- Skip common real-world places with standard translations, such as New York, unless snippets show special media-specific meaning.
- Skip ordinary street names or addresses, such as Madison Avenue, unless used as a recurring concept or organization.
- Skip malformed phrases such as Have Carbone; return the meaningful proper noun instead, such as Carbone, only if it needs consistency guidance.
- Skip common English words only capitalized because they start a sentence.
- Skip generic speaker labels and caption descriptions, such as MAN, WOMAN, Door Opens, and Phone Ringing.
- Skip common abbreviations with stable obvious translations, such as OK or TV, unless they are media-specific.
- Skip phrases whose target translation should vary by sentence.`
```

- [ ] **Step 5: Format and run prompt tests**

Run:

```bash
gofmt -w internal/service/glossary/types.go internal/service/glossary/llm_openai.go internal/service/glossary/llm_openai_test.go
go test ./internal/service/glossary -run 'GlossaryPrompt|OpenAICompatibleClientGenerateGlossary|GenerateResponseValidate' -v
```

Expected: PASS.

- [ ] **Step 6: Commit prompt changes**

Run:

```bash
git add internal/service/glossary/types.go internal/service/glossary/llm_openai.go internal/service/glossary/llm_openai_test.go
git commit -m "feat: tighten glossary llm selection prompt"
```

Expected: commit succeeds with only these three files staged. If `llm_openai.go` or its test already contains unrelated local edits, stage only the hunks from this task.

---

### Task 4: Skip Generated Contextual Entries During Injection

**Files:**
- Modify: `internal/service/glossary/prompt_builder_test.go`
- Modify: `internal/service/glossary/prompt_builder.go`

- [ ] **Step 1: Add fixed-terminology eligibility tests**

Append to `internal/service/glossary/prompt_builder_test.go`:

```go
func TestBuildTerminologySkipsGeneratedContextualEntries(t *testing.T) {
	got := BuildTerminology([]PromptEntry{
		{
			Scope:           ScopeMedia,
			MediaKey:        "m",
			NormalizedTerm:  "madison avenue",
			DisplayTerm:     "Madison Avenue",
			TargetText:      "麦迪逊大道",
			TranslationMode: TranslationModeContextual,
			Confidence:      0.95,
			Status:          StatusActive,
			Source:          SourceGenerated,
		},
	}, PromptOptions{MediaKey: "m", InjectMinConfidence: 0.80, MaxPromptEntries: 10})

	if !reflect.DeepEqual(got, []Terminology(nil)) {
		t.Fatalf("terminology = %#v, want nil", got)
	}
}

func TestBuildTerminologyAllowsCuratedContextualEntries(t *testing.T) {
	got := BuildTerminology([]PromptEntry{
		{
			Scope:           ScopeMedia,
			MediaKey:        "m",
			NormalizedTerm:  "madison avenue",
			DisplayTerm:     "Madison Avenue",
			TargetText:      "麦迪逊大道",
			TranslationMode: TranslationModeContextual,
			Confidence:      0.60,
			Status:          StatusActive,
			Source:          SourceCurated,
		},
	}, PromptOptions{MediaKey: "m", InjectMinConfidence: 0.80, MaxPromptEntries: 10})
	want := []Terminology{{Source: "Madison Avenue", Target: "麦迪逊大道"}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("terminology = %#v, want %#v", got, want)
	}
}
```

- [ ] **Step 2: Run prompt builder tests and verify failure**

Run:

```bash
go test ./internal/service/glossary -run BuildTerminology -v
```

Expected: FAIL because generated contextual entries are currently eligible.

- [ ] **Step 3: Update prompt eligibility**

In `internal/service/glossary/prompt_builder.go`, add this check in `isPromptEligible` after the empty term/target check:

```go
	if entry.TranslationMode == TranslationModeContextual && entry.Source != SourceCurated {
		return false
	}
```

The full function should read:

```go
func isPromptEligible(entry PromptEntry, opts PromptOptions) bool {
	if entry.Status != StatusActive {
		return false
	}
	if strings.TrimSpace(entry.NormalizedTerm) == "" || strings.TrimSpace(entry.TargetText) == "" {
		return false
	}
	if entry.TranslationMode == TranslationModeContextual && entry.Source != SourceCurated {
		return false
	}
	if entry.Confidence < opts.InjectMinConfidence && entry.Source != SourceCurated {
		return false
	}
	if entry.Scope == ScopeMedia && entry.MediaKey != opts.MediaKey {
		return false
	}
	return entry.Scope == ScopeMedia || entry.Scope == ScopeCommon
}
```

- [ ] **Step 4: Format and run prompt builder tests**

Run:

```bash
gofmt -w internal/service/glossary/prompt_builder.go internal/service/glossary/prompt_builder_test.go
go test ./internal/service/glossary -run BuildTerminology -v
```

Expected: PASS.

- [ ] **Step 5: Commit injection safety change**

Run:

```bash
git add internal/service/glossary/prompt_builder.go internal/service/glossary/prompt_builder_test.go
git commit -m "feat: skip contextual generated terminology"
```

Expected: commit succeeds with only prompt builder files staged.

---

### Task 5: Verify Integrated Glossary Behavior

**Files:**
- Read: `git status --short`
- Read: `git show --stat --oneline HEAD`

- [ ] **Step 1: Run focused combined tests**

Run:

```bash
go test ./internal/service/glossary -run 'ExtractCandidates|GlossaryPrompt|BuildTerminology' -v
```

Expected: PASS.

- [ ] **Step 2: Run related service tests**

Run:

```bash
go test ./internal/service/glossary ./internal/service/worker ./internal/service/translator
```

Expected: PASS.

- [ ] **Step 3: Check working tree**

Run:

```bash
git status --short
```

Expected: no unstaged files from this plan. Pre-existing unrelated modified files may still appear if they existed before execution; do not revert them.

- [ ] **Step 4: Inspect latest implementation commit**

Run:

```bash
git show --stat --oneline HEAD
```

Expected: latest commit is `feat: skip contextual generated terminology` and includes only `internal/service/glossary/prompt_builder.go` and `internal/service/glossary/prompt_builder_test.go`.
