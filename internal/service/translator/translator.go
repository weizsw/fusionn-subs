package translator

import (
	"regexp"
	"strconv"
	"strings"
)

type Terminology struct {
	Source string
	Target string
}

func combineInstructions(base, extra string) string {
	base = strings.TrimSpace(base)
	extra = strings.TrimSpace(extra)

	switch {
	case base == "":
		return extra
	case extra == "":
		return base
	default:
		return base + "\n\n" + extra
	}
}

func appendTerminologyArgs(args []string, req Request) []string {
	for _, term := range req.Terminology {
		source := strings.TrimSpace(term.Source)
		target := strings.TrimSpace(term.Target)
		if source == "" || target == "" {
			continue
		}
		args = append(args, "--terminology", source+"::"+target)
	}
	if req.BuildTerminologyMap {
		args = append(args, "--build-terminology-map")
	}
	return args
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

// separatedAPIKeyPattern and equalsAPIKeyPattern find credential flag values
// in a rendered command line.
var separatedAPIKeyPattern = regexp.MustCompile(`((?:-k|--apikey)\s+)(\S+)`)
var equalsAPIKeyPattern = regexp.MustCompile(`((?:-k|--apikey)=)(\S+)`)

// maskAPIKeyInCommand renders credential flag values as the shared "***"
// placeholder, so a log line never carries even a prefix of a secret.
func maskAPIKeyInCommand(cmd string) string {
	cmd = separatedAPIKeyPattern.ReplaceAllStringFunc(cmd, func(match string) string {
		parts := separatedAPIKeyPattern.FindStringSubmatch(match)
		if len(parts) == 3 {
			return parts[1] + "***"
		}
		return match
	})
	return equalsAPIKeyPattern.ReplaceAllStringFunc(cmd, func(match string) string {
		parts := equalsAPIKeyPattern.FindStringSubmatch(match)
		if len(parts) == 3 {
			return parts[1] + "***"
		}
		return match
	})
}

// fatalIndicators mark a failed translation regardless of the process exit code.
var fatalIndicators = []string{
	"translationimpossibleerror",
	"failed to translate",
	"failed to communicate with provider",
	"saving partial results",
	"traceback (most recent call last)",
}

func detectScriptFailure(stdoutStr, stderrStr string) (string, bool) {
	combined := strings.ToLower(stdoutStr + "\n" + stderrStr)
	for _, indicator := range fatalIndicators {
		if strings.Contains(combined, indicator) {
			return indicator, true
		}
	}
	return "", false
}
