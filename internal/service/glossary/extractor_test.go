package glossary

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractCandidatesFromSRT(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "episode.srt")
	content := `1
00:00:01,000 --> 00:00:03,000
SO15 asked DCI Carey to review the feed.

2
00:00:04,000 --> 00:00:06,000
SO15 said Counter Terrorism Command was involved.

3
00:00:07,000 --> 00:00:09,000
Carey called SO15 again.
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	candidates, err := ExtractCandidates(path, ExtractOptions{
		MaxSubtitleBytes:        1 << 20,
		MaxCues:                 100,
		MaxCandidates:           20,
		MaxSnippetsPerCandidate: 2,
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	found := map[string]Candidate{}
	for _, c := range candidates {
		found[c.NormalizedTerm] = c
	}
	if found["so15"].Frequency != 3 {
		t.Fatalf("SO15 frequency = %d", found["so15"].Frequency)
	}
	if len(found["so15"].Snippets) != 2 {
		t.Fatalf("SO15 snippets = %#v", found["so15"].Snippets)
	}
	if _, ok := found["dci"]; !ok {
		t.Fatalf("expected DCI candidate, got %#v", found)
	}
}

func TestExtractCandidatesHonorsSubtitleSizeLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "episode.srt")
	if err := os.WriteFile(path, []byte("1234567890"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := ExtractCandidates(path, ExtractOptions{MaxSubtitleBytes: 5})
	if err == nil {
		t.Fatal("expected size limit error")
	}
}
