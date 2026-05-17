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
	for _, key := range stableExternalIDPriority(msg.MediaType) {
		if value := externalIDValue(msg.ExternalIDs, key); value != "" {
			return MediaKey{
				Value:  key + ":" + value,
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
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if normalizedKey != externalIDSonarr && normalizedKey != externalIDRadarr {
			continue
		}
		if value := strings.TrimSpace(msg.ExternalIDs[key]); value != "" {
			return MediaKey{
				Value:  normalizedKey + ":" + strings.ToLower(value),
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
			Value:  scopedMediaID(source, mediaID),
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

func stableExternalIDPriority(mediaType string) []string {
	switch normalizeKeyPart(mediaType) {
	case "movie":
		return []string{externalIDTMDB, externalIDIMDB, externalIDTVDB}
	default:
		return []string{externalIDTVDB, externalIDTMDB, externalIDIMDB}
	}
}

func externalIDValue(ids map[string]string, wantKey string) string {
	for key, value := range ids {
		if strings.EqualFold(strings.TrimSpace(key), wantKey) {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func scopedMediaID(source, mediaID string) string {
	mediaID = strings.ToLower(strings.TrimSpace(mediaID))
	if prefix, value, ok := strings.Cut(mediaID, ":"); ok {
		prefix = normalizeKeyPart(prefix)
		value = strings.TrimSpace(value)
		if prefix != "" && value != "" {
			return prefix + ":" + value
		}
	}
	return source + ":" + mediaID
}

func normalizeKeyPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = nonKeyChars.ReplaceAllString(value, "-")
	return strings.Trim(value, "-")
}
