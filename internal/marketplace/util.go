package marketplace

import (
	"regexp"
	"strings"
)

var nonKebab = regexp.MustCompile(`[^a-z0-9]+`)

// kebab normalizes a skill name to lowercase kebab-case for the install
// directory name (FR-12). It collapses any run of non-alphanumerics to a single
// hyphen and trims leading/trailing hyphens.
func kebab(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonKebab.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
