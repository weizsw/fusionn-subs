package version

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Version holds the current build version. Override with
// -ldflags "-X github.com/fusionn-subs/internal/version.Version=v1.2.3".
var Version = "dev"

const (
	separator = "────────────────────────────────────────────────────────────"
	banner    = `
   ___           _                                   _         
  / _|_   _ ___(_) ___  _ __  _ __        ___ _   _| |__  ___ 
 | |_| | | / __| |/ _ \| '_ \| '_ \ _____/ __| | | | '_ \/ __|
 |  _| |_| \__ \ | (_) | | | | | | |_____\__ \ |_| | |_) \__ \
 |_|  \__,_|___/_|\___/|_| |_|_| |_|     |___/\__,_|_.__/|___/
`
)

// Banner returns the ASCII-art project banner.
func Banner() string {
	return strings.Trim(banner, "\n")
}

// PrintBanner writes the decorated banner and version info to w (stdout if nil).
func PrintBanner(w io.Writer) {
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, separator)
	fmt.Fprintln(w, Banner())
	fmt.Fprintf(w, "\n  fusionn-subs %s\n", Version)
	fmt.Fprintf(w, "  Subtitle Translation Worker\n")
	fmt.Fprintln(w, separator)
	fmt.Fprintln(w)
}
