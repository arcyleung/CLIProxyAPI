package codexquota

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientFetchAndConsume(t *testing.T) {
	var consumeCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Fatalf("missing auth header")
		}
		if r.Header.Get("Chatgpt-Account-Id") != "acct" {
			t.Fatalf("missing account header")
		}
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/usage"):
			_, _ = io.WriteString(w, `{
				"rate_limit":{"secondary_window":{"used_percent":100,"limit_window_seconds":604800}},
				"additional_rate_limits":[{"limit_name":"GPT-5.3-Codex-Spark","rate_limit":{"secondary_window":{"used_percent":100,"limit_window_seconds":604800}}}],
				"rate_limit_reset_credits":{"available_count":1}
			}`)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "consume"):
			consumeCalls++
			_, _ = io.WriteString(w, `{"code":"reset","windows_reset":2}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := &Client{
		HTTP:       server.Client(),
		UsageURL:   server.URL + "/usage",
		ConsumeURL: server.URL + "/consume",
	}
	usage, err := client.FetchUsage(context.Background(), "tok", "acct")
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if !ShouldConsumeBankedReset(usage) {
		t.Fatalf("expected eligible usage, got %#v", usage)
	}
	windows, err := client.ConsumeResetCredit(context.Background(), "tok", "acct")
	if err != nil {
		t.Fatalf("ConsumeResetCredit: %v", err)
	}
	if windows != 2 || consumeCalls != 1 {
		t.Fatalf("windows=%d consumeCalls=%d", windows, consumeCalls)
	}
}
