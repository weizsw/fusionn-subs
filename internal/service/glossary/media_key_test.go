package glossary

import (
	"testing"

	"github.com/fusionn-subs/internal/types"
)

func TestResolveMediaKeyPrefersStableExternalID(t *testing.T) {
	msg := types.JobMessage{
		MediaTitle:  "The Capture",
		MediaType:   "series",
		ExternalIDs: map[string]string{"imdb": "tt8201186", "tvdb": "355620"},
	}

	key := ResolveMediaKey(msg)
	if key.Value != "tvdb:355620" || key.Source != MediaKeySourceExternalID {
		t.Fatalf("key = %#v", key)
	}
}

func TestResolveMediaKeyUsesSourceScopedMediaID(t *testing.T) {
	msg := types.JobMessage{
		MediaTitle:   "The Capture",
		MediaType:    "series",
		MediaID:      "42",
		SourceSystem: "sonarr",
	}

	key := ResolveMediaKey(msg)
	if key.Value != "sonarr:42" || key.Source != MediaKeySourceMediaID {
		t.Fatalf("key = %#v", key)
	}
}

func TestResolveMediaKeyFallsBackToTitle(t *testing.T) {
	msg := types.JobMessage{MediaTitle: "The Capture", MediaType: "Series"}

	key := ResolveMediaKey(msg)
	if key.Value != "title:series:the-capture" || key.Source != MediaKeySourceTitle {
		t.Fatalf("key = %#v", key)
	}
}

func TestResolveMediaKeyFallsBackToPathHash(t *testing.T) {
	msg := types.JobMessage{SubtitlePath: "/media/The Capture/S01E01.eng.srt"}

	key := ResolveMediaKey(msg)
	if key.Source != MediaKeySourcePath || key.Value == "" {
		t.Fatalf("key = %#v", key)
	}
}
