package types

import (
	"errors"
	"path/filepath"
	"strings"
)

type JobMessage struct {
	FileName  string `json:"file_name"`
	Path      string `json:"path"`
	VideoPath string `json:"video_path"`
	Overview  string `json:"overview"`
	Provider  string `json:"provider"`
}

func (m JobMessage) Validate() error {
	if strings.TrimSpace(m.Path) == "" {
		return errors.New("message.path is required")
	}

	return nil
}

func (m JobMessage) OutputPath(suffix string) string {
	if suffix == "" {
		return m.Path
	}

	cleanSuffix := strings.TrimPrefix(suffix, ".")
	if cleanSuffix == "" {
		return m.Path
	}

	replacement := cleanSuffix
	if dot := strings.Index(replacement, "."); dot != -1 {
		replacement = replacement[:dot]
	}

	lowerPath := strings.ToLower(m.Path)
	if idx := strings.LastIndex(lowerPath, ".eng"); idx != -1 {
		prefix := m.Path[:idx+1]
		suffixPart := m.Path[idx+4:]
		return prefix + replacement + suffixPart
	}

	if strings.HasSuffix(lowerPath, ".srt") {
		trimmed := m.Path[:len(m.Path)-len(".srt")]
		return trimmed + "." + cleanSuffix
	}

	ext := filepath.Ext(m.Path)
	if ext != "" {
		base := strings.TrimSuffix(m.Path, ext)
		return base + "." + cleanSuffix + ext
	}

	return m.Path + "." + cleanSuffix
}
