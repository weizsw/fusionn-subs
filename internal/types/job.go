package types

import (
	"errors"
	"path/filepath"
	"strings"
)

type JobMessage struct {
	JobID        string `json:"job_id"`
	VideoPath    string `json:"video_path"`
	SubtitlePath string `json:"subtitle_path"`
	MediaTitle   string `json:"media_title"`
	MediaType    string `json:"media_type"`
}

func (m JobMessage) Validate() error {
	if strings.TrimSpace(m.JobID) == "" {
		return errors.New("job_id is required")
	}
	if strings.TrimSpace(m.VideoPath) == "" {
		return errors.New("video_path is required")
	}
	if strings.TrimSpace(m.SubtitlePath) == "" {
		return errors.New("subtitle_path is required")
	}

	return nil
}

func (m JobMessage) OutputPath(suffix string) string {
	if suffix == "" {
		return m.SubtitlePath
	}

	cleanSuffix := strings.TrimPrefix(suffix, ".")
	if cleanSuffix == "" {
		return m.SubtitlePath
	}

	replacement := cleanSuffix
	if dot := strings.Index(replacement, "."); dot != -1 {
		replacement = replacement[:dot]
	}

	lowerPath := strings.ToLower(m.SubtitlePath)
	if idx := strings.LastIndex(lowerPath, ".eng"); idx != -1 {
		prefix := m.SubtitlePath[:idx+1]
		suffixPart := m.SubtitlePath[idx+4:]
		return prefix + replacement + suffixPart
	}

	if strings.HasSuffix(lowerPath, ".srt") {
		trimmed := m.SubtitlePath[:len(m.SubtitlePath)-len(".srt")]
		return trimmed + "." + cleanSuffix + ".srt"
	}

	ext := filepath.Ext(m.SubtitlePath)
	if ext != "" {
		base := strings.TrimSuffix(m.SubtitlePath, ext)
		return base + "." + cleanSuffix + ext
	}

	return m.SubtitlePath + "." + cleanSuffix
}
