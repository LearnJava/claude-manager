package logship

import (
	"regexp"
	"strings"
)

const masked = "***"

// secretRes match credentials that must never reach the logs repository.
var secretRes = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[A-Za-z0-9+/=\s]*`), // block cut off by a chunk end
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]+`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`gh[po]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`\b\d{8,10}:[A-Za-z0-9_-]{35}\b`),
}

// Mask replaces known secret shapes and every non-empty string of extra with
// "***". Everything written into the logs clone passes through it.
func Mask(s string, extra []string) string {
	for _, e := range extra {
		if strings.TrimSpace(e) != "" {
			s = strings.ReplaceAll(s, e, masked)
		}
	}
	for _, re := range secretRes {
		s = re.ReplaceAllString(s, masked)
	}
	return s
}
