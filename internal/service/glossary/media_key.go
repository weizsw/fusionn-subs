package glossary

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"

	"github.com/fusionn-subs/internal/types"
)

var nonKeyChars = regexp.MustCompile(`[^a-z0-9]+`)

func ResolveMediaKey(msg types.JobMessage) MediaKey {
	for _, key := range []string{externalIDTVDB, externalIDTMDB, externalIDIMDB} {
		if value := strings.TrimSpace(msg.ExternalIDs[key]); value != "" {
			return MediaKey{
				Value:  key + ":" + strings.ToLower(value),
				Source: MediaKeySourceExternalID,
			}
		}
	}

	keys := make([]string, 0, len(msg.ExternalIDs))
	for key := range msg.ExternalIDs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key != externalIDSonarr && key != externalIDRadarr {
			continue
		}
		if value := strings.TrimSpace(msg.ExternalIDs[key]); value != "" {
			return MediaKey{
				Value:  key + ":" + strings.ToLower(value),
				Source: MediaKeySourceExternalID,
			}
		}
	}

	if mediaID := strings.TrimSpace(msg.MediaID); mediaID != "" {
		source := strings.ToLower(strings.TrimSpace(msg.SourceSystem))
		if source == "" {
			source = defaultMediaIDSource
		}
		return MediaKey{
			Value:  source + ":" + strings.ToLower(mediaID),
			Source: MediaKeySourceMediaID,
		}
	}

	if title := normalizeKeyPart(msg.MediaTitle); title != "" {
		mediaType := normalizeKeyPart(msg.MediaType)
		if mediaType == "" {
			mediaType = unknownMediaType
		}
		return MediaKey{
			Value:  "title:" + mediaType + ":" + title,
			Source: MediaKeySourceTitle,
		}
	}

	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(msg.SubtitlePath))))
	return MediaKey{
		Value:  "path:" + hex.EncodeToString(sum[:8]),
		Source: MediaKeySourcePath,
	}
}

func normalizeKeyPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = nonKeyChars.ReplaceAllString(value, "-")
	return strings.Trim(value, "-")
}
