package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ZulipConfig is the goquest bot's Zulip identity. All three fields must be
// set for the notifier to be enabled; otherwise it does nothing.
type ZulipConfig struct {
	URL      string // e.g. https://zulip.toji.homes (no trailing /api)
	BotEmail string
	APIKey   string
}

// Zulip sends direct messages as the goquest bot. Information only: nothing it
// sends can be answered to decide anything (decisions go through the API/UI so
// the decider is derived server-side).
type Zulip struct {
	cfg ZulipConfig
	hc  *http.Client
}

func NewZulip(cfg ZulipConfig) *Zulip {
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	return &Zulip{cfg: cfg, hc: &http.Client{Timeout: 8 * time.Second}}
}

// Enabled reports whether the bot is fully configured.
func (z *Zulip) Enabled() bool {
	return z != nil && z.cfg.URL != "" && z.cfg.BotEmail != "" && z.cfg.APIKey != ""
}

const zulipMaxContent = 9000 // Zulip's limit is 10000 characters

// SendDM sends content to one user by email. When the bot is not configured it
// is a no-op returning nil, so callers never need to guard.
func (z *Zulip) SendDM(ctx context.Context, toEmail, content string) error {
	if !z.Enabled() {
		return nil
	}
	toEmail = strings.TrimSpace(toEmail)
	if toEmail == "" || !strings.Contains(toEmail, "@") {
		return errors.New("zulip: recipient email required")
	}
	if len(content) > zulipMaxContent {
		content = content[:zulipMaxContent]
	}
	to, _ := json.Marshal([]string{toEmail})
	form := url.Values{"type": {"private"}, "to": {string(to)}, "content": {content}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, z.cfg.URL+"/api/v1/messages", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth(z.cfg.BotEmail, z.cfg.APIKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := z.hc.Do(req)
	if err != nil {
		return fmt.Errorf("zulip send: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var out struct {
		Result string `json:"result"`
		Msg    string `json:"msg"`
	}
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode/100 != 2 || out.Result != "success" {
		// never log the request (it carries the API key in the auth header only,
		// but keep bodies out of logs anyway) — status and Zulip's message suffice
		return fmt.Errorf("zulip send: status %d: %s", resp.StatusCode, truncate(out.Msg, 200))
	}
	return nil
}

// LogIfDisabled logs once at startup which mode the notifier is in.
func (z *Zulip) LogIfDisabled() {
	if !z.Enabled() {
		log.Printf("zulip notifier disabled (ZULIP_URL / ZULIP_BOT_EMAIL / ZULIP_BOT_API_KEY not all set)")
	}
}
