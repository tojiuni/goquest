package vcs_test

import (
	"testing"

	"github.com/tojiuni/goquest/internal/vcs"
)

// Quality gate for the VCS title_name matcher (ExtractTitleNames). This is a
// golden-corpus regression gate in the spirit of gopedia's Gardener: it measures
// match accuracy (precision/recall) and dedup correctness over a curated corpus
// and fails CI if either metric regresses below threshold. A change that makes
// the matcher miss real title_names (recall↓) or invent spurious ones (precision↓)
// breaks the build here, before it can mis-link — or fail to link — production
// tickets.
const matchThreshold = 1.0 // curated corpus: the matcher must be exact.

type matchCase struct {
	name  string
	texts []string
	want  []string // expected title_names, in first-seen order
}

// corpus mixes realistic branch/PR/commit texts with adversarial non-matches.
var corpus = []matchCase{
	{"branch-plain", []string{"goq-vault-portforward"}, []string{"goq-vault-portforward"}},
	{"branch-feature-prefix", []string{"feature/goq-otel-tracing"}, []string{"goq-otel-tracing"}},
	{"pr-title-two-domains", []string{"implement goq-idempotency-key and gop-memory"}, []string{"goq-idempotency-key", "gop-memory"}},
	{"commit-multiline", []string{"fix neunex-cli-sso\n\nlong body text"}, []string{"neunex-cli-sso"}},
	{"digits-in-token", []string{"gop-2026-backfill"}, []string{"gop-2026-backfill"}},
	{"path-two-tokens", []string{"who-login-v2/goq-checkout"}, []string{"who-login-v2", "goq-checkout"}},
	{"no-hyphen-no-match", []string{"hotfix urgent nothing"}, nil},
	{"uppercase-no-match", []string{"FIX-BUG RELEASE-NOTES"}, nil},
	{"empty-text", []string{""}, nil},
	{"dedup-within-text", []string{"goq-foo goq-foo goq-foo"}, []string{"goq-foo"}},
	{"dedup-across-texts", []string{"goq-bar", "please review goq-bar now"}, []string{"goq-bar"}},
	{"mixed-match-and-noise", []string{"WIP: goq-risk-tier-gate (draft)"}, []string{"goq-risk-tier-gate"}},
}

func toSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func TestVCSMatcher_QualityGate(t *testing.T) {
	var tp, fp, fn int
	for _, c := range corpus {
		got := vcs.ExtractTitleNames(c.texts...)

		// Dedup metric: the matcher must never emit a title_name twice.
		if len(toSet(got)) != len(got) {
			t.Errorf("[%s] duplicate in output: %v", c.name, got)
		}

		want := toSet(c.want)
		gotSet := toSet(got)
		for g := range gotSet {
			if want[g] {
				tp++
			} else {
				fp++
			}
		}
		for w := range want {
			if !gotSet[w] {
				fn++
			}
		}
	}

	precision := 1.0
	if tp+fp > 0 {
		precision = float64(tp) / float64(tp+fp)
	}
	recall := 1.0
	if tp+fn > 0 {
		recall = float64(tp) / float64(tp+fn)
	}
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	t.Logf("VCS matcher quality: precision=%.3f recall=%.3f f1=%.3f (tp=%d fp=%d fn=%d, cases=%d)",
		precision, recall, f1, tp, fp, fn, len(corpus))

	if precision < matchThreshold {
		t.Errorf("precision %.3f < threshold %.3f — matcher is inventing spurious title_names", precision, matchThreshold)
	}
	if recall < matchThreshold {
		t.Errorf("recall %.3f < threshold %.3f — matcher is missing real title_names", recall, matchThreshold)
	}
}
