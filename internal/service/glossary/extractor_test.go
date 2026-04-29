package glossary

import (
	"os"
	"path/filepath"
	"testing"
)

func extractCandidateMap(t *testing.T, filename, content string, opts ExtractOptions) map[string]Candidate {
	t.Helper()

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

	path := filepath.Join(t.TempDir(), filename)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	candidates, err := ExtractCandidates(path, opts)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	found := map[string]Candidate{}
	for _, c := range candidates {
		found[c.NormalizedTerm] = c
	}
	return found
}

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

func TestExtractCandidatesIncludesBrandLikeSingletons(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
She packed Louboutins beside an iPhone.
`, ExtractOptions{})

	if _, ok := found["louboutins"]; !ok {
		t.Fatalf("expected Louboutins candidate, got %#v", found)
	}
	if _, ok := found["iphone"]; !ok {
		t.Fatalf("expected iPhone candidate, got %#v", found)
	}
}

func TestExtractCandidatesIncludesRepeatedSingleProperNouns(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
Carey reviewed the case.

2
00:00:04,000 --> 00:00:06,000
SO15 briefed Carey again.
`, ExtractOptions{})

	if found["carey"].Frequency != 2 {
		t.Fatalf("Carey frequency = %d, got %#v", found["carey"].Frequency, found)
	}
}

func TestExtractCandidatesFiltersMalformedPhraseAndKeepsMeaningfulName(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
Have Carbone cater the event.
`, ExtractOptions{})

	if _, ok := found["have carbone"]; ok {
		t.Fatalf("did not expect Have Carbone candidate, got %#v", found)
	}
	if _, ok := found["carbone"]; !ok {
		t.Fatalf("expected Carbone candidate, got %#v", found)
	}
}

func TestExtractCandidatesFiltersCommonSingleWordNoise(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
Well, look, okay, thanks, yeah, you found the case.
`, ExtractOptions{})

	for _, term := range []string{"well", "look", "okay", "thanks", "yeah", "you", "the"} {
		if _, ok := found[term]; ok {
			t.Fatalf("did not expect %q candidate, got %#v", term, found)
		}
	}
}

func TestExtractCandidatesHandlesASSBilingualLinesAndStyleOverrides(t *testing.T) {
	found := extractCandidateMap(t, "episode.ass", `[Script Info]
ScriptType: v4.00+

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,10,1
Style: Default_1,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,让 Carbone 承办餐饮\N{\rDefault_1}Have Carbone cater.
`, ExtractOptions{})

	if _, ok := found["default"]; ok {
		t.Fatalf("did not expect Default style candidate, got %#v", found)
	}
	if _, ok := found["have carbone"]; ok {
		t.Fatalf("did not expect Have Carbone candidate, got %#v", found)
	}
	if _, ok := found["carbone"]; !ok {
		t.Fatalf("expected Carbone candidate, got %#v", found)
	}
}

func TestExtractCandidatesFiltersSpeakerLabelsAndCaptionOnlyLines(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
MAN: SO15 arrived.

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

func TestExtractCandidatesFiltersCaptionOnlyLinesAfterSpeakerLabels(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
MAN: [Door Opens]
`, ExtractOptions{})

	if _, ok := found["door opens"]; ok {
		t.Fatalf("did not expect Door Opens candidate, got %#v", found)
	}
}

func TestExtractCandidatesFiltersNamedSpeakerLabels(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
CAREY: SO15 arrived.

2
00:00:04,000 --> 00:00:06,000
CAREY: We need help.
`, ExtractOptions{})

	if _, ok := found["carey"]; ok {
		t.Fatalf("did not expect Carey speaker label candidate, got %#v", found)
	}
	if _, ok := found["so15"]; !ok {
		t.Fatalf("expected SO15 candidate, got %#v", found)
	}
}

func TestExtractCandidatesKeepsNonSpeakerColonContent(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
Case: DCI Carey briefed SO15.
`, ExtractOptions{})

	if _, ok := found["dci carey"]; !ok {
		t.Fatalf("expected DCI Carey candidate, got %#v", found)
	}
	if _, ok := found["so15"]; !ok {
		t.Fatalf("expected SO15 candidate, got %#v", found)
	}
}

func TestExtractCandidatesIncludesExpandedTermShapes(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
Spider-Man met O'Neill near MI6 for G-Force at Studio 54.

2
00:00:04,000 --> 00:00:06,000
S.H.I.E.L.D. called AT&T and R&D at Bank of America.

3
00:00:07,000 --> 00:00:09,000
Dr. House briefed DCI Carey about Hermès.
`, ExtractOptions{})

	for _, term := range []string{"spider-man", "o'neill", "mi6", "g-force", "studio 54", "s.h.i.e.l.d.", "at&t", "r&d", "bank of america", "dr. house", "dci carey", "hermès"} {
		if _, ok := found[term]; !ok {
			t.Fatalf("expected %q candidate, got %#v", term, found)
		}
	}
}

func TestExtractCandidatesNormalizesPossessiveVariants(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
Carbone's team met Carbone.

2
00:00:04,000 --> 00:00:06,000
O'Neill's file named O'Neill.
`, ExtractOptions{})

	if found["carbone"].Frequency != 2 {
		t.Fatalf("Carbone frequency = %d, got %#v", found["carbone"].Frequency, found)
	}
	if found["o'neill"].Frequency != 2 {
		t.Fatalf("O'Neill frequency = %d, got %#v", found["o'neill"].Frequency, found)
	}
}

func TestExtractCandidatesPrioritizesHighRiskTermsBeforeCommonPhrases(t *testing.T) {
	found := extractCandidateMap(t, "episode.srt", `1
00:00:01,000 --> 00:00:03,000
New York and Madison Avenue saw SO15, Louboutins, and Spider-Man.

2
00:00:04,000 --> 00:00:06,000
New York stayed on the list.

3
00:00:07,000 --> 00:00:09,000
New York came up again.
`, ExtractOptions{MaxCandidates: 3})

	for _, term := range []string{"so15", "louboutins", "spider-man"} {
		if _, ok := found[term]; !ok {
			t.Fatalf("expected %q candidate, got %#v", term, found)
		}
	}
	for _, term := range []string{"new york", "madison avenue"} {
		if _, ok := found[term]; ok {
			t.Fatalf("did not expect %q candidate, got %#v", term, found)
		}
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
