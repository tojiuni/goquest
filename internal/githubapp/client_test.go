package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(block))
}

// fakeGitHub serves both the installation-token endpoint and the contents
// endpoint, recording the App JWT it saw on the token exchange.
func fakeGitHub(t *testing.T, contentByPath map[string]string) (*httptest.Server, *string) {
	t.Helper()
	var seenAppAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			seenAppAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      "inst-tok-1",
				"expires_at": "2099-01-01T00:00:00Z",
			})
		case strings.Contains(r.URL.Path, "/contents/"):
			path := r.URL.Path[strings.Index(r.URL.Path, "/contents/")+len("/contents/"):]
			content, ok := contentByPath[path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content":  base64.StdEncoding.EncodeToString([]byte(content)),
				"encoding": "base64",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return ts, &seenAppAuth
}

func TestContent_ReturnsDecodedFile(t *testing.T) {
	ts, seenAppAuth := fakeGitHub(t, map[string]string{"goquest/spec/foo.md": "# Foo\n"})
	defer ts.Close()

	c, err := newClientWithBaseURL(ts.URL, "app-1", "inst-1", testPrivateKeyPEM(t), ts.Client())
	require.NoError(t, err)

	content, err := c.Content(context.Background(), "tojiuni", "geneso", "goquest/spec/foo.md")
	require.NoError(t, err)
	assert.Equal(t, "# Foo\n", content)
	assert.Contains(t, *seenAppAuth, "Bearer ", "installation-token exchange must present the App JWT")
}

func TestContent_ReusesInstallationTokenAcrossCalls(t *testing.T) {
	tokenCalls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			tokenCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "inst-tok-1", "expires_at": "2099-01-01T00:00:00Z"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": base64.StdEncoding.EncodeToString([]byte("x")), "encoding": "base64",
		})
	}))
	defer ts.Close()

	c, err := newClientWithBaseURL(ts.URL, "app-1", "inst-1", testPrivateKeyPEM(t), ts.Client())
	require.NoError(t, err)

	_, err = c.Content(context.Background(), "tojiuni", "geneso", "a.md")
	require.NoError(t, err)
	_, err = c.Content(context.Background(), "tojiuni", "geneso", "b.md")
	require.NoError(t, err)
	assert.Equal(t, 1, tokenCalls, "installation token must be cached and reused")
}

func TestContent_NotFound_ReturnsErrNotFound(t *testing.T) {
	ts, _ := fakeGitHub(t, map[string]string{})
	defer ts.Close()

	c, err := newClientWithBaseURL(ts.URL, "app-1", "inst-1", testPrivateKeyPEM(t), ts.Client())
	require.NoError(t, err)

	_, err = c.Content(context.Background(), "tojiuni", "geneso", "missing.md")
	require.ErrorIs(t, err, ErrNotFound)
}
