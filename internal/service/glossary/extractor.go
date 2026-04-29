package glossary

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	astisub "github.com/asticode/go-astisub"
)

var candidatePattern = regexp.MustCompile(`\b(?:[A-Z]{2,}\d*|[A-Z][a-z]+(?:\s+[A-Z][a-z]+){1,3})\b`)

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
	for _, item := range subs.Items {
		text := strings.TrimSpace(item.String())
		if text == "" {
			continue
		}
		for _, term := range candidatePattern.FindAllString(text, -1) {
			normalized := normalizeTerm(term)
			if normalized == "" {
				continue
			}
			candidate := byTerm[normalized]
			if candidate == nil {
				candidate = &Candidate{Term: term, NormalizedTerm: normalized}
				byTerm[normalized] = candidate
			}
			candidate.Frequency++
			if opts.MaxSnippetsPerCandidate <= 0 || len(candidate.Snippets) < opts.MaxSnippetsPerCandidate {
				candidate.Snippets = append(candidate.Snippets, text)
			}
		}
	}

	out := make([]Candidate, 0, len(byTerm))
	for _, candidate := range byTerm {
		out = append(out, *candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Frequency == out[j].Frequency {
			return out[i].NormalizedTerm < out[j].NormalizedTerm
		}
		return out[i].Frequency > out[j].Frequency
	})
	if opts.MaxCandidates > 0 && len(out) > opts.MaxCandidates {
		out = out[:opts.MaxCandidates]
	}
	return out, nil
}

func normalizeTerm(term string) string {
	term = strings.ToLower(strings.TrimSpace(term))
	return strings.Join(strings.Fields(term), " ")
}
