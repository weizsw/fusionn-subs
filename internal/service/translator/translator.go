package translator

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/fusionn-subs/internal/util"
	"github.com/fusionn-subs/pkg/logger"
)

// ANSI escape codes for dimmed output (like Docker build logs)
const (
	dimStart = "\033[2m"
	dimEnd   = "\033[0m"
)

// executeScript executes a script command and handles stdout/stderr streaming
func executeScript(cmd *exec.Cmd, outputPath string) (string, error) {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("stderr pipe: %w", err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	var wg sync.WaitGroup

	// Stream output in dim/grey (like Docker build logs)
	wg.Add(2)
	go func() {
		defer wg.Done()
		streamDimmed(stdoutPipe, &stdoutBuf)
	}()
	go func() {
		defer wg.Done()
		streamDimmed(stderrPipe, &stderrBuf)
	}()

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start script: %w", err)
	}

	wg.Wait()
	err = cmd.Wait()

	stdoutStr := strings.TrimSpace(stdoutBuf.String())
	stderrStr := strings.TrimSpace(stderrBuf.String())

	if err != nil {
		logger.Errorf("Translation failed: %v", err)
		if stderrStr != "" {
			logger.Errorf("Script stderr: %s", stderrStr)
		}
		return "", fmt.Errorf("script failed: %w", err)
	}

	if _, statErr := os.Stat(outputPath); statErr != nil {
		logger.Errorf("Output file not found after script completed")
		return "", fmt.Errorf("output not found: %w", statErr)
	}

	if reason, failed := detectScriptFailure(stdoutStr, stderrStr); failed {
		logger.Errorf("Script failure detected: %s", reason)
		return "", fmt.Errorf("script reported failure: %s", reason)
	}

	logger.Infof("✅ Translation completed: %s", outputPath)
	return outputPath, nil
}

func buildCommandLine(cmd string, args []string) string {
	// Pre-allocate capacity
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(cmd))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}
	if !strings.ContainsAny(arg, " \t\n\"'\\") {
		return arg
	}
	return strconv.Quote(arg)
}

var apiKeyPattern = regexp.MustCompile(`(-k\s+)(\S+)`)

// maskAPIKeyInCommand replaces -k value with masked version for logging
func maskAPIKeyInCommand(cmd string) string {
	return apiKeyPattern.ReplaceAllStringFunc(cmd, func(match string) string {
		parts := apiKeyPattern.FindStringSubmatch(match)
		if len(parts) == 3 {
			return parts[1] + util.MaskSecret(parts[2])
		}
		return match
	})
}

// streamDimmed reads from r, writes to buf for capture, and prints dimmed to stderr.
// This creates a Docker-build-like experience where script output is visible but greyed out.
func streamDimmed(r io.Reader, buf *bytes.Buffer) {
	scanner := bufio.NewScanner(r)
	// Increase buffer for potentially long lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		buf.WriteString(line)
		buf.WriteByte('\n')
		// Print dimmed to stderr (doesn't interfere with structured logs)
		fmt.Fprintf(os.Stderr, "%s  │ %s%s\n", dimStart, line, dimEnd)
	}

	if err := scanner.Err(); err != nil {
		logger.Debugf("Scanner error (may be normal): %v", err)
	}
}

func detectScriptFailure(stdoutStr, stderrStr string) (string, bool) {
	fatalIndicators := []string{
		"translationimpossibleerror",
		"failed to translate",
		"failed to communicate with provider",
		"saving partial results",
		"traceback (most recent call last)",
	}

	combined := strings.ToLower(stdoutStr + "\n" + stderrStr)
	for _, indicator := range fatalIndicators {
		if strings.Contains(combined, indicator) {
			return indicator, true
		}
	}
	return "", false
}
