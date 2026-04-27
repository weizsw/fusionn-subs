package types

import (
	"encoding/json"
	"testing"
)

func TestJobMessageUnmarshalOptionalGlossaryIdentity(t *testing.T) {
	raw := []byte(`{
		"job_id":"job-1",
		"video_path":"/media/The Capture/S01E01.mkv",
		"subtitle_path":"/media/The Capture/S01E01.eng.srt",
		"media_title":"The Capture",
		"media_type":"series",
		"media_id":"sonarr:42",
		"source_system":"sonarr",
		"external_ids":{"tvdb":"355620","imdb":"tt8201186"},
		"season":1,
		"episode":1
	}`)

	var msg JobMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if msg.ExternalIDs["tvdb"] != "355620" {
		t.Fatalf("tvdb id = %q", msg.ExternalIDs["tvdb"])
	}
	if msg.Season != 1 || msg.Episode != 1 {
		t.Fatalf("season/episode = %d/%d", msg.Season, msg.Episode)
	}
	if err := msg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestJobMessageUnmarshalExistingPayloadStillWorks(t *testing.T) {
	raw := []byte(`{
		"job_id":"job-legacy",
		"video_path":"/media/movie.mkv",
		"subtitle_path":"/media/movie.eng.srt",
		"media_title":"Movie",
		"media_type":"movie"
	}`)

	var msg JobMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := msg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if msg.ExternalIDs != nil {
		t.Fatalf("external ids should be nil for legacy payload")
	}
}
