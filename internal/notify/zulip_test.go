package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestZulip_DisabledIsNoop(t *testing.T) {
	for _, z := range []*Zulip{nil, NewZulip(ZulipConfig{}), NewZulip(ZulipConfig{URL: "http://x", BotEmail: "b@x"})} {
		if z.Enabled() {
			t.Error("must be disabled unless all three settings are present")
		}
		if err := z.SendDM(context.Background(), "u@x.io", "hi"); err != nil {
			t.Errorf("disabled SendDM must be a no-op, got %v", err)
		}
	}
}

func TestZulip_SendDM(t *testing.T) {
	var gotUser, gotPass string
	var form map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/messages" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		gotUser, gotPass, _ = r.BasicAuth()
		_ = r.ParseForm()
		form = map[string]string{"type": r.Form.Get("type"), "to": r.Form.Get("to"), "content": r.Form.Get("content")}
		json.NewEncoder(w).Encode(map[string]string{"result": "success"})
	}))
	defer srv.Close()
	z := NewZulip(ZulipConfig{URL: srv.URL + "/", BotEmail: "bot@x", APIKey: "k"})
	if err := z.SendDM(context.Background(), "owner@x.io", "hello"); err != nil {
		t.Fatal(err)
	}
	if gotUser != "bot@x" || gotPass != "k" || form["type"] != "private" || form["to"] != `["owner@x.io"]` || form["content"] != "hello" {
		t.Errorf("bad request: %s %s %+v", gotUser, gotPass, form)
	}
}

func TestZulip_Failures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"result": "error", "msg": "bad recipient"})
	}))
	defer srv.Close()
	z := NewZulip(ZulipConfig{URL: srv.URL, BotEmail: "bot@x", APIKey: "secret-key"})
	err := z.SendDM(context.Background(), "u@x.io", "hi")
	if err == nil || !strings.Contains(err.Error(), "bad recipient") {
		t.Fatalf("want error with Zulip's message, got %v", err)
	}
	if strings.Contains(err.Error(), "secret-key") {
		t.Error("error must not leak the API key")
	}
	if err := z.SendDM(context.Background(), "not-an-email", "hi"); err == nil {
		t.Error("non-email recipient must be refused")
	}
}
