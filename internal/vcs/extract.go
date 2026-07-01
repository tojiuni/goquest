package vcs

import "regexp"

// titleNameTokenRe is the unanchored form of the title_name format
// ^[a-z][a-z0-9]*(-[a-z0-9]+)+$ used to scan tokens out of free text.
var titleNameTokenRe = regexp.MustCompile(`[a-z][a-z0-9]*(?:-[a-z0-9]+)+`)

// ExtractTitleNames returns the distinct title_name-shaped tokens found across
// all input texts, in first-seen order.
func ExtractTitleNames(texts ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range texts {
		for _, m := range titleNameTokenRe.FindAllString(t, -1) {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}
