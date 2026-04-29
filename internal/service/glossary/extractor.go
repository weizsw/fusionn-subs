package glossary

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	astisub "github.com/asticode/go-astisub"
)

const (
	candidatePriorityAcronym = iota
	candidatePriorityBrand
	candidatePrioritySpecial
	candidatePriorityTitle
	candidatePrioritySingleProper
	candidatePriorityPhrase
)

var (
	candidatePatterns = []candidatePatternSpec{
		{regexp.MustCompile(`\b(?:[A-Z]\.){2,}(?:[A-Z]\.)?`), candidatePriorityAcronym},
		{regexp.MustCompile(`\b(?:[A-Z]{2,}\d*|[A-Z]+\d+|\d*[A-Z]{2,})\b`), candidatePriorityAcronym},
		{regexp.MustCompile(`\b(?:[a-z]+[A-Z][A-Za-z]*|[A-Z][\p{Ll}\p{M}]{3,}s)(?:['’]s)?\b`), candidatePriorityBrand},
		{regexp.MustCompile(`\b[A-Za-z]+&[A-Za-z]+(?:&[A-Za-z]+)*\b`), candidatePrioritySpecial},
		{regexp.MustCompile(`\b(?:[A-Z][\p{Ll}\p{M}]+|[A-Z]+)(?:-(?:[A-Z][\p{Ll}\p{M}]+|[A-Z]+|\d+))+\b`), candidatePrioritySpecial},
		{regexp.MustCompile(`\b[A-Z][\p{L}\p{M}]*['’][A-Z][\p{Ll}\p{M}]+(?:['’]s)?\b`), candidatePrioritySpecial},
		{regexp.MustCompile(`\b(?:Dr\.|DCI)\s+[A-Z][\p{Ll}\p{M}]+(?:['’]s)?\b`), candidatePriorityTitle},
		{regexp.MustCompile(`\b[A-Z][\p{Ll}\p{M}]+(?:\s+(?:of|the|for|de|del|la|le|van|von|du)\s+[A-Z][\p{Ll}\p{M}]+)+\b`), candidatePriorityPhrase},
		{regexp.MustCompile(`\b[A-Z][\p{Ll}\p{M}]+(?:\s+(?:[A-Z][\p{Ll}\p{M}]+|\d+)){1,3}\b`), candidatePriorityPhrase},
		{regexp.MustCompile(`\b(?:[a-z]+[A-Z][A-Za-z]*|[A-Z][\p{Ll}\p{M}]+)(?:['’]s)?\b`), candidatePrioritySingleProper},
	}
	speakerPrefixPattern = regexp.MustCompile(`(?i)^\s*(?:MAN|WOMAN|ANNOUNCER|TV|RADIO|BOTH|ALL):\s*`)
	spacePattern         = regexp.MustCompile(`\s+`)
	possessivePattern    = regexp.MustCompile(`(?i)(?:'s|’s)\b`)
	camelCasePattern     = regexp.MustCompile(`[a-z][A-Z]`)
)

type candidatePatternSpec struct {
	re       *regexp.Regexp
	priority int
}

type candidateMatch struct {
	term            string
	normalizedTerm  string
	start           int
	end             int
	priority        int
	sentenceInitial bool
}

type candidateState struct {
	candidate       Candidate
	priority        int
	sentenceInitial bool
	firstSeen       int
}

func ExtractCandidates(path string, opts ExtractOptions) ([]Candidate, error) {
	if opts.MaxSubtitleBytes > 0 {
		stat, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat subtitle: %w", err)
		}
		if stat.Size() > opts.MaxSubtitleBytes {
			return nil, fmt.Errorf("subtitle too large: %d bytes > %d", stat.Size(), opts.MaxSubtitleBytes)
		}
	}

	subs, err := astisub.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("parse subtitle: %w", err)
	}
	if opts.MaxCues > 0 && len(subs.Items) > opts.MaxCues {
		return nil, fmt.Errorf("too many subtitle cues: %d > %d", len(subs.Items), opts.MaxCues)
	}

	byTerm := make(map[string]*Candidate)
	states := make(map[string]*candidateState)
	seenIndex := 0
	for _, item := range subs.Items {
		for _, line := range item.Lines {
			snippet := strings.TrimSpace(line.String())
			text := prepareCandidateLine(snippet)
			if text == "" {
				continue
			}
			for _, match := range collectCandidateMatches(text) {
				normalized := match.normalizedTerm
				if normalized == "" {
					continue
				}
				state := states[normalized]
				if state == nil {
					state = &candidateState{
						candidate: Candidate{
							Term:           displayTermForCandidate(match.term),
							NormalizedTerm: normalized,
						},
						priority:        match.priority,
						sentenceInitial: match.sentenceInitial,
						firstSeen:       seenIndex,
					}
					states[normalized] = state
					seenIndex++
				}
				if match.priority < state.priority {
					state.priority = match.priority
					state.candidate.Term = displayTermForCandidate(match.term)
				}
				state.sentenceInitial = state.sentenceInitial && match.sentenceInitial
				state.candidate.Frequency++
				if opts.MaxSnippetsPerCandidate <= 0 || len(state.candidate.Snippets) < opts.MaxSnippetsPerCandidate {
					state.candidate.Snippets = append(state.candidate.Snippets, snippet)
				}
			}
		}
	}

	for normalized, state := range states {
		if !shouldKeepCandidate(state) {
			continue
		}
		candidate := state.candidate
		byTerm[normalized] = &candidate
	}

	out := make([]Candidate, 0, len(byTerm))
	for _, candidate := range byTerm {
		out = append(out, *candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		left := states[out[i].NormalizedTerm]
		right := states[out[j].NormalizedTerm]
		if out[i].Frequency != out[j].Frequency {
			return out[i].Frequency > out[j].Frequency
		}
		if left.priority != right.priority {
			return left.priority < right.priority
		}
		if left.firstSeen != right.firstSeen {
			return left.firstSeen < right.firstSeen
		}
		return out[i].NormalizedTerm < out[j].NormalizedTerm
	})
	if opts.MaxCandidates > 0 && len(out) > opts.MaxCandidates {
		out = out[:opts.MaxCandidates]
	}
	return out, nil
}

func prepareCandidateLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || isCaptionOnlyLine(line) {
		return ""
	}
	line = speakerPrefixPattern.ReplaceAllString(line, "")

	var b strings.Builder
	for _, r := range line {
		switch {
		case unicode.Is(unicode.Latin, r), unicode.IsDigit(r), unicode.IsSpace(r):
			b.WriteRune(r)
		case r == '\'' || r == '’' || r == '-' || r == '.' || r == '&':
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.TrimSpace(spacePattern.ReplaceAllString(b.String(), " "))
}

func isCaptionOnlyLine(line string) bool {
	line = strings.TrimSpace(line)
	if len(line) < 2 {
		return false
	}
	pairs := map[byte]byte{'(': ')', '[': ']'}
	close, ok := pairs[line[0]]
	if !ok || line[len(line)-1] != close {
		return false
	}
	inner := strings.TrimSpace(line[1 : len(line)-1])
	if inner == "" {
		return false
	}
	words := strings.Fields(inner)
	if len(words) > 4 {
		return false
	}
	for _, word := range words {
		word = strings.Trim(word, ".,!?")
		if word == "" {
			return false
		}
		r, _ := utf8.DecodeRuneInString(word)
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func collectCandidateMatches(line string) []candidateMatch {
	var matches []candidateMatch
	for _, spec := range candidatePatterns {
		for _, loc := range spec.re.FindAllStringIndex(line, -1) {
			term := strings.TrimSpace(line[loc[0]:loc[1]])
			if term == "" || isMalformedPhrase(term) || hasDanglingConnector(term) {
				continue
			}
			normalized := normalizeTerm(term)
			if normalized == "" {
				continue
			}
			matches = append(matches, candidateMatch{
				term:            term,
				normalizedTerm:  normalized,
				start:           loc[0],
				end:             loc[1],
				priority:        spec.priority,
				sentenceInitial: isSentenceInitial(line, loc[0]),
			})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].start != matches[j].start {
			return matches[i].start < matches[j].start
		}
		if matches[i].end != matches[j].end {
			return matches[i].end > matches[j].end
		}
		return matches[i].priority < matches[j].priority
	})

	var out []candidateMatch
	seenSpans := map[[2]int]bool{}
	for _, match := range matches {
		span := [2]int{match.start, match.end}
		if seenSpans[span] {
			continue
		}
		if isSingleToken(match.term) && coveredByKeptMatch(match, out) {
			continue
		}
		seenSpans[span] = true
		out = append(out, match)
	}
	return out
}

func coveredByKeptMatch(match candidateMatch, kept []candidateMatch) bool {
	for _, other := range kept {
		if match.start < other.start || match.end > other.end || match.normalizedTerm == other.normalizedTerm {
			continue
		}
		if match.priority == candidatePriorityAcronym && !hasSpecialPunctuation(other.term) {
			continue
		}
		if !isSingleToken(other.term) || hasSpecialPunctuation(other.term) {
			return true
		}
	}
	return false
}

func hasSpecialPunctuation(term string) bool {
	return strings.ContainsAny(term, "-'’&.")
}

func shouldKeepCandidate(state *candidateState) bool {
	normalized := state.candidate.NormalizedTerm
	if normalized == "" || commonSingleWordNoise[normalized] {
		return false
	}
	if !isSingleToken(normalized) {
		return true
	}
	switch state.priority {
	case candidatePriorityAcronym, candidatePriorityBrand, candidatePrioritySpecial:
		return true
	case candidatePrioritySingleProper:
		return state.candidate.Frequency > 1 || !state.sentenceInitial || isBrandLikeSingleton(state.candidate.Term)
	default:
		return false
	}
}

func displayTermForCandidate(term string) string {
	return stripPossessive(strings.TrimSpace(term))
}

func normalizeTerm(term string) string {
	term = strings.ToLower(displayTermForCandidate(term))
	term = spacePattern.ReplaceAllString(strings.TrimSpace(term), " ")
	return term
}

func stripPossessive(term string) string {
	return possessivePattern.ReplaceAllString(term, "")
}

func isMalformedPhrase(term string) bool {
	parts := strings.Fields(term)
	return len(parts) == 2 && malformedPhraseStarters[strings.ToLower(parts[0])]
}

func hasDanglingConnector(term string) bool {
	parts := strings.Fields(strings.ToLower(term))
	if len(parts) == 0 {
		return true
	}
	return connectorWords[parts[0]] || connectorWords[parts[len(parts)-1]]
}

func isSentenceInitial(line string, start int) bool {
	return strings.TrimSpace(line[:start]) == ""
}

func isSingleToken(term string) bool {
	return !strings.Contains(strings.TrimSpace(term), " ")
}

func isBrandLikeSingleton(term string) bool {
	term = displayTermForCandidate(term)
	if camelCasePattern.MatchString(term) {
		return true
	}
	return strings.HasSuffix(term, "s") && len(term) > 4
}

var commonSingleWordNoise = map[string]bool{
	"look":   true,
	"okay":   true,
	"thanks": true,
	"the":    true,
	"well":   true,
	"yeah":   true,
	"you":    true,
}

var malformedPhraseStarters = map[string]bool{
	"come": true,
	"get":  true,
	"go":   true,
	"have": true,
	"let":  true,
	"look": true,
	"see":  true,
	"tell": true,
	"well": true,
}

var connectorWords = map[string]bool{
	"de":  true,
	"del": true,
	"du":  true,
	"for": true,
	"la":  true,
	"le":  true,
	"of":  true,
	"the": true,
	"van": true,
	"von": true,
}
