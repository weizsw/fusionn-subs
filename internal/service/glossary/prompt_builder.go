package glossary

import (
	"sort"
	"strings"
)

func BuildTerminology(entries []PromptEntry, opts PromptOptions) []Terminology {
	if opts.MaxPromptEntries <= 0 {
		return nil
	}

	winners := make(map[string]PromptEntry)
	for _, entry := range entries {
		if !isPromptEligible(entry, opts) {
			continue
		}
		term := strings.TrimSpace(entry.NormalizedTerm)
		current, ok := winners[term]
		if !ok || promptRank(entry, opts.MediaKey) > promptRank(current, opts.MediaKey) {
			winners[term] = entry
		}
	}

	selected := make([]PromptEntry, 0, len(winners))
	for _, entry := range winners {
		selected = append(selected, entry)
	}
	sort.SliceStable(selected, func(i, j int) bool {
		return promptRank(selected[i], opts.MediaKey) > promptRank(selected[j], opts.MediaKey)
	})
	if len(selected) > opts.MaxPromptEntries {
		selected = selected[:opts.MaxPromptEntries]
	}

	terminology := make([]Terminology, 0, len(selected))
	for _, entry := range selected {
		source := promptDisplayTerm(entry)
		target := strings.TrimSpace(entry.TargetText)
		if source == "" || target == "" {
			continue
		}
		terminology = append(terminology, Terminology{
			Source: source,
			Target: target,
		})
	}
	if len(terminology) == 0 {
		return nil
	}
	return terminology
}

func isPromptEligible(entry PromptEntry, opts PromptOptions) bool {
	if entry.Status != StatusActive {
		return false
	}
	if strings.TrimSpace(entry.NormalizedTerm) == "" || strings.TrimSpace(entry.TargetText) == "" {
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

func promptDisplayTerm(entry PromptEntry) string {
	if display := strings.TrimSpace(entry.DisplayTerm); display != "" {
		return display
	}
	return strings.TrimSpace(entry.NormalizedTerm)
}

func promptRank(entry PromptEntry, mediaKey string) int {
	score := 0
	if entry.Scope == ScopeMedia && entry.MediaKey == mediaKey {
		score += 1_000_000
	}
	if entry.Source == SourceCurated {
		score += 100_000
	}
	score += int(entry.Confidence * 10_000)
	score += entry.EvidenceCount * 100
	score += int(entry.LastSeenAt.Unix() % 100)
	return score
}
