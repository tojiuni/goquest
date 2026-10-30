package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func classifyWith(t *testing.T, allowed []string, prep func(ctx context.Context) context.Context) PrincipalKind {
	t.Helper()
	var got PrincipalKind
	h := ClassifyPrincipal(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = PrincipalKindFrom(r)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(prep(req.Context()))
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestClassifyPrincipal(t *testing.T) {
	ui := []string{"goquest-ui"}
	jwtAs := func(aud []string, email string, verified bool) func(context.Context) context.Context {
		return func(ctx context.Context) context.Context {
			ctx = WithRequesterID(ctx, "someone")
			return WithJWTIdentity(ctx, &JWTIdentity{Audience: aud, Email: email, EmailVerified: verified})
		}
	}
	cases := []struct {
		name    string
		allowed []string
		prep    func(context.Context) context.Context
		want    PrincipalKind
	}{
		{"ui login", ui, jwtAs([]string{"goquest-ui"}, "u@x.io", true), PrincipalHuman},
		{"ui client but unverified email", ui, jwtAs([]string{"goquest-ui"}, "u@x.io", false), PrincipalSession},
		{"ui client but no email", ui, jwtAs([]string{"goquest-ui"}, "", true), PrincipalSession},
		{"device-code cli token", ui, jwtAs([]string{"lymphhub-cli"}, "u@x.io", true), PrincipalSession},
		{"toji-cli device token", ui, jwtAs([]string{"toji-cli"}, "u@x.io", true), PrincipalSession},
		{"machine token", ui, jwtAs([]string{"tokenhub-refresher"}, "", false), PrincipalSession},
		{"aud array containing ui client", ui, jwtAs([]string{"goquest-ui", "other"}, "u@x.io", true), PrincipalSession},
		{"no aud", ui, jwtAs(nil, "u@x.io", true), PrincipalSession},
		{"empty allow list: nobody human", nil, jwtAs([]string{"goquest-ui"}, "u@x.io", true), PrincipalSession},
		{"api key even with ui-looking identity", ui, func(ctx context.Context) context.Context {
			ctx = jwtAs([]string{"goquest-ui"}, "u@x.io", true)(ctx)
			return context.WithValue(ctx, apiKeyMetaCtxKey, &APIKeyMeta{})
		}, PrincipalOther},
		{"customer token", ui, func(ctx context.Context) context.Context {
			return WithCustomerCtx(jwtAs([]string{"goquest-ui"}, "u@x.io", true)(ctx), &CustomerCtx{})
		}, PrincipalCustomer},
		{"lymphhub session / skip-auth: requester but no jwt identity", ui, func(ctx context.Context) context.Context {
			return WithRequesterID(ctx, "dev-user")
		}, PrincipalOther},
		{"unauthenticated", ui, func(ctx context.Context) context.Context { return ctx }, PrincipalOther},
	}
	for _, c := range cases {
		if got := classifyWith(t, c.allowed, c.prep); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func TestPrincipalKindDefaultsToOther(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if got := PrincipalKindFrom(r); got != PrincipalOther {
		t.Errorf("unset kind = %s, want other", got)
	}
}
