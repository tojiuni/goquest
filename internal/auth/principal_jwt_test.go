package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// End to end through the real JWT middleware: the identity the classifier
// reads must come from the validated token, not from anything the client sends.
func TestMiddlewareThenClassify_RealJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	defer jwks.Close()

	sign := func(aud []string, email string, verified bool) string {
		c := jwt.MapClaims{"sub": "11111111-2222-3333-4444-555555555555", "aud": aud,
			"iss": "https://idp.test", "exp": time.Now().Add(time.Hour).Unix()}
		if email != "" {
			c["email"], c["email_verified"] = email, verified
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
		tok.Header["kid"] = "k1"
		s, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	var gotKind PrincipalKind
	var gotID string
	h := Middleware(jwks.URL, false)(ClassifyPrincipal([]string{"goquest-ui"})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotKind, gotID = PrincipalKindFrom(r), RequesterID(r)
		})))

	for _, c := range []struct {
		name     string
		token    string
		wantKind PrincipalKind
		wantID   string
	}{
		{"ui login", sign([]string{"goquest-ui"}, "u@x.io", true), PrincipalHuman, "u@x.io"},
		{"device-code token with same email", sign([]string{"lymphhub-cli"}, "u@x.io", true), PrincipalSession, "u@x.io"},
		{"machine token", sign([]string{"tokenhub-refresher"}, "", false), PrincipalSession, "11111111-2222-3333-4444-555555555555"},
	} {
		gotKind, gotID = "", ""
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+c.token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 || gotKind != c.wantKind || gotID != c.wantID {
			t.Errorf("%s: code=%d kind=%s id=%s; want kind=%s id=%s", c.name, rec.Code, gotKind, gotID, c.wantKind, c.wantID)
		}
	}
}
