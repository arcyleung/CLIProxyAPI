package auth

import (
	"context"
	"testing"
)

func TestAuthAutoUseBankedResetEnabled(t *testing.T) {
	if AuthAutoUseBankedResetEnabled(nil) {
		t.Fatal("nil auth should be disabled")
	}
	if AuthAutoUseBankedResetEnabled(&Auth{Provider: "codex"}) {
		t.Fatal("default should be disabled")
	}
	if !AuthAutoUseBankedResetEnabled(&Auth{Metadata: map[string]any{"auto_use_banked_reset": true}}) {
		t.Fatal("metadata true should enable")
	}
	if !AuthAutoUseBankedResetEnabled(&Auth{Attributes: map[string]string{"auto_use_banked_reset": "true"}}) {
		t.Fatal("attribute true should enable")
	}
	if AuthAutoUseBankedResetEnabled(&Auth{Attributes: map[string]string{"auto_use_banked_reset": "false"}}) {
		t.Fatal("attribute false should disable")
	}
}

func TestTryCodexBankedResetOnUsageLimitGates(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	auth := &Auth{
		ID:       "codex-1",
		Provider: "codex",
		Metadata: map[string]any{
			"access_token":       "tok",
			"account_id":         "acct",
			"auto_use_banked_reset": true,
		},
		Attributes: map[string]string{"auto_use_banked_reset": "true"},
	}
	if manager.tryCodexBankedResetOnUsageLimit(context.Background(), auth, nil) {
		t.Fatal("nil error should not consume")
	}
	if manager.tryCodexBankedResetOnUsageLimit(context.Background(), auth, context.Canceled) {
		t.Fatal("non usage-limit error should not consume")
	}
	auth.Metadata["auto_use_banked_reset"] = false
	auth.Attributes["auto_use_banked_reset"] = "false"
	if manager.tryCodexBankedResetOnUsageLimit(context.Background(), auth, errUsageLimit()) {
		t.Fatal("opted-out auth should not consume")
	}
}

type usageLimitErr struct{}

func (usageLimitErr) Error() string { return `{"error":{"type":"usage_limit_reached"}}` }

func errUsageLimit() error { return usageLimitErr{} }
