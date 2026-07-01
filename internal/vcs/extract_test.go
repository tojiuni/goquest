package vcs

import (
	"reflect"
	"testing"
)

func TestExtractTitleNames(t *testing.T) {
	cases := []struct {
		name  string
		texts []string
		want  []string
	}{
		{"branch plain", []string{"goq-vault-portforward"}, []string{"goq-vault-portforward"}},
		{"branch prefixed", []string{"feature/goq-vault-portforward"}, []string{"goq-vault-portforward"}},
		{"commit msg", []string{"fix login for goq-auth-bug and who-render"}, []string{"goq-auth-bug", "who-render"}},
		{"dedup across sources", []string{"goq-x", "did goq-x"}, []string{"goq-x"}},
		{"no hyphen -> none", []string{"goq", "main", "HEAD"}, nil},
		{"uppercase -> none", []string{"GOQ-FOO"}, nil}, // title_name is lowercase; a token needs a lowercase start + an internal hyphen
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractTitleNames(c.texts...)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ExtractTitleNames(%v) = %v, want %v", c.texts, got, c.want)
			}
		})
	}
}
