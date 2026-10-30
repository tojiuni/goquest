package auth

import (
	"context"
	"net/http"
)

// PrincipalKind says what sort of caller authenticated the request. It exists
// for decisions that must be made by a person (approvals): RequesterID alone
// cannot tell a person from an agent, because a shared API key resolves to a
// user's email and a user's device-code JWT carries a verified email too.
//
// The zero/unset value is PrincipalOther, so a request that no classifier
// touched is never treated as human (fail closed).
type PrincipalKind string

const (
	// PrincipalHuman: an interactive login to a trusted UI client (see
	// ClassifyPrincipal).
	PrincipalHuman PrincipalKind = "human"
	// PrincipalSession: a machine token — a session account's client_credentials
	// JWT, which carries no email claim at all. Only this kind may act on a
	// delegation rule. A person's token (device-code CLI, grafana, argocd, ...)
	// is never a session, even though it is not an interactive goquest-ui login.
	PrincipalSession PrincipalKind = "session"
	// PrincipalCustomer: a magic-link customer token.
	PrincipalCustomer PrincipalKind = "customer"
	// PrincipalOther: API key, lymphhub CLI session, dev skip-auth, or unknown.
	PrincipalOther PrincipalKind = "other"
)

type principalKindKeyT struct{}

var principalKindKey principalKindKeyT

const jwtIdentityKey contextKey = "jwt_identity"

// JWTIdentity is the part of a validated bearer JWT that classification needs.
type JWTIdentity struct {
	Audience      []string
	Email         string
	EmailVerified bool
}

// WithPrincipalKind returns a copy of ctx carrying k. For tests and middleware.
func WithPrincipalKind(ctx context.Context, k PrincipalKind) context.Context {
	return context.WithValue(ctx, principalKindKey, k)
}

// WithJWTIdentity returns a copy of ctx carrying id. For tests.
func WithJWTIdentity(ctx context.Context, id *JWTIdentity) context.Context {
	return context.WithValue(ctx, jwtIdentityKey, id)
}

// PrincipalKindFrom returns the classified kind, or PrincipalOther if none was set.
func PrincipalKindFrom(r *http.Request) PrincipalKind {
	if k, ok := r.Context().Value(principalKindKey).(PrincipalKind); ok && k != "" {
		return k
	}
	return PrincipalOther
}

// ClassifyPrincipal records the PrincipalKind after authentication has run.
//
// A request is human only if it was authenticated by a bearer JWT whose email
// is verified and whose audience is exactly one value, and that value is in
// humanAudiences. A JWT with no email claim at all is a machine (session)
// token; every other JWT that names a person is PrincipalOther (the OIDC client ids of the interactive UI, e.g.
// "goquest-ui"). lymphhub sets aud to the client's own id unless the client
// has an audience override, so a token obtained through the UI client has
// aud=<ui client>, while device-code and client_credentials tokens carry their
// own client ids. An empty humanAudiences means nobody is human.
//
// Operating rule: never configure an audience override that yields a trusted
// UI client id on any other OIDC client; goquest cannot see the grant type.
func ClassifyPrincipal(humanAudiences []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(humanAudiences))
	for _, a := range humanAudiences {
		if a != "" {
			allowed[a] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(WithPrincipalKind(r.Context(), classify(r, allowed))))
		})
	}
}

func classify(r *http.Request, allowed map[string]struct{}) PrincipalKind {
	if CustomerFrom(r) != nil {
		return PrincipalCustomer
	}
	if KeyMeta(r) != nil {
		return PrincipalOther // API key, however it is named
	}
	id, _ := r.Context().Value(jwtIdentityKey).(*JWTIdentity)
	if id == nil || RequesterID(r) == "" {
		return PrincipalOther // lymphhub ls_ session, skip-auth, or unauthenticated
	}
	if id.Email == "" && !id.EmailVerified {
		return PrincipalSession // machine token: no email identity at all
	}
	if id.EmailVerified && id.Email != "" && len(id.Audience) == 1 {
		if _, ok := allowed[id.Audience[0]]; ok {
			return PrincipalHuman
		}
	}
	// A token that names a person (email present) but is not an interactive
	// goquest-ui login: another app's token or a device-code token held by a
	// session. It must not be able to decide anything.
	return PrincipalOther
}
