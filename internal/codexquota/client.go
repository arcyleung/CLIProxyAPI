package codexquota

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	UsageURL   = "https://chatgpt.com/backend-api/wham/usage"
	ConsumeURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
	userAgent  = "cli-proxy-api-codex-quota/1.0"
)

// HTTPDoer is the subset of http.Client used by the Codex quota client.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client talks to ChatGPT Codex usage and banked-reset endpoints.
type Client struct {
	HTTP       HTTPDoer
	Timeout    time.Duration
	UsageURL   string // optional override for tests
	ConsumeURL string // optional override for tests
}

// FetchUsage loads current Codex usage for the given access token.
func (c *Client) FetchUsage(ctx context.Context, accessToken, accountID string) (Usage, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return Usage{}, fmt.Errorf("codexquota: access token is required")
	}
	urlStr := UsageURL
	if c != nil && strings.TrimSpace(c.UsageURL) != "" {
		urlStr = strings.TrimSpace(c.UsageURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return Usage{}, err
	}
	c.applyHeaders(req, accessToken, accountID)
	body, status, err := c.do(req)
	if err != nil {
		return Usage{}, err
	}
	if status < 200 || status >= 300 {
		return Usage{}, fmt.Errorf("codexquota: usage HTTP %d: %s", status, summarizeBody(body))
	}
	return ParseUsage(body), nil
}

// ConsumeResetCredit spends one banked rate-limit reset credit.
func (c *Client) ConsumeResetCredit(ctx context.Context, accessToken, accountID string) (windowsReset int, err error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return 0, fmt.Errorf("codexquota: access token is required")
	}
	redeemID, err := newRedeemRequestID()
	if err != nil {
		return 0, err
	}
	payload, _ := json.Marshal(map[string]string{"redeem_request_id": redeemID})
	urlStr := ConsumeURL
	if c != nil && strings.TrimSpace(c.ConsumeURL) != "" {
		urlStr = strings.TrimSpace(c.ConsumeURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	c.applyHeaders(req, accessToken, accountID)
	req.Header.Set("Content-Type", "application/json")
	body, status, err := c.do(req)
	if err != nil {
		return 0, err
	}
	if status < 200 || status >= 300 {
		return 0, fmt.Errorf("codexquota: consume HTTP %d: %s", status, summarizeBody(body))
	}
	code := strings.TrimSpace(gjsonGet(body, "code"))
	if !strings.EqualFold(code, "reset") {
		return 0, fmt.Errorf("codexquota: unexpected consume response code %q", code)
	}
	if !gjsonExists(body, "windows_reset") {
		return 0, fmt.Errorf("codexquota: consume response missing windows_reset")
	}
	return int(gjsonInt(body, "windows_reset")), nil
}

func (c *Client) applyHeaders(req *http.Request, accessToken, accountID string) {
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if accountID = strings.TrimSpace(accountID); accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}
}

func (c *Client) do(req *http.Request) ([]byte, int, error) {
	client := c.HTTP
	if client == nil {
		timeout := c.Timeout
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func newRedeemRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func summarizeBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 240 {
		return s[:240] + "..."
	}
	return s
}
