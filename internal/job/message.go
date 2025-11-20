package job

import (
	"errors"
	"path/filepath"
	"strings"
)

type Message struct {
	FileName  string `json:"file_name"`
	Path      string `json:"path"`
	VideoPath string `json:"video_path"`
	Overview  string `json:"overview"`
	Provider  string `json:"provider"`
}

func (m Message) Validate() error {
	if strings.TrimSpace(m.Path) == "" {
		return errors.New("message.path is required")
	}

	return nil
}

func (m Message) OutputPath(suffix string) string {
	if suffix == "" {
		return m.Path
	}

	cleanSuffix := strings.TrimPrefix(suffix, ".")
	if cleanSuffix == "" {
		return m.Path
	}

	lowerPath := strings.ToLower(m.Path)
	if strings.HasSuffix(lowerPath, ".eng.srt") {
		trimmed := m.Path[:len(m.Path)-len(".eng.srt")]
		return trimmed + "." + cleanSuffix
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
