package auth

import (
	"context"
	"strings"
	"sync"
	"time"

	"net/http"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/codexquota"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
)

const (
	codexBankedResetAttrKey     = "auto_use_banked_reset"
	codexBankedResetMinInterval = 30 * time.Second
)

var (
	codexBankedResetInFlight sync.Map // authID -> struct{}
	codexBankedResetLastAt   sync.Map // authID -> time.Time
)

// AuthAutoUseBankedResetEnabled reports whether this auth opted into automatic banked reset consumption.
// Prefer AuthAutoUseBankedResetEnabled; AuthAutoResetCreditsEnabled remains as a compatibility alias.
func AuthAutoUseBankedResetEnabled(auth *Auth) bool {
	if auth == nil {
		return false
	}
	if auth.Attributes != nil {
		if raw := strings.TrimSpace(auth.Attributes[codexBankedResetAttrKey]); raw != "" {
			return isTruthy(raw)
		}
	}
	if auth.Metadata != nil {
		switch v := auth.Metadata[codexBankedResetAttrKey].(type) {
		case bool:
			return v
		case string:
			return isTruthy(v)
		case float64:
			return v != 0
		}
	}
	return false
}

// AuthAutoResetCreditsEnabled is a compatibility alias for AuthAutoUseBankedResetEnabled.
func AuthAutoResetCreditsEnabled(auth *Auth) bool {
	return AuthAutoUseBankedResetEnabled(auth)
}

func isTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func isCodexProvider(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "codex")
}

func isUsageLimitReachedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "usage_limit_reached")
}

// tryCodexBankedResetOnUsageLimit consumes one banked reset when the account opted in and
// both weekly bars are depleted. On success it clears local quota/cooldown state.
// Returns true only when a banked reset was actually consumed.
func (m *Manager) tryCodexBankedResetOnUsageLimit(ctx context.Context, auth *Auth, execErr error) bool {
	if m == nil || auth == nil || execErr == nil {
		return false
	}
	if !isCodexProvider(auth.Provider) || !isUsageLimitReachedError(execErr) {
		return false
	}
	if !AuthAutoUseBankedResetEnabled(auth) {
		return false
	}
	authID := strings.TrimSpace(auth.ID)
	if authID == "" {
		return false
	}
	if _, loaded := codexBankedResetInFlight.LoadOrStore(authID, struct{}{}); loaded {
		return false
	}
	defer codexBankedResetInFlight.Delete(authID)

	if lastRaw, ok := codexBankedResetLastAt.Load(authID); ok {
		if last, okTime := lastRaw.(time.Time); okTime && time.Since(last) < codexBankedResetMinInterval {
			return false
		}
	}
	codexBankedResetLastAt.Store(authID, time.Now())

	token, accountID := codexAccessCredentials(auth)
	if token == "" {
		log.WithField("auth_id", authID).Debug("codex banked reset skipped: missing access token")
		return false
	}

	var cfg *internalconfig.Config
	if loaded, ok := m.runtimeConfig.Load().(*internalconfig.Config); ok {
		cfg = loaded
	}
	client := &codexquota.Client{
		HTTP: newCodexQuotaHTTPClient(cfg, auth, 20*time.Second),
	}
	usage, errUsage := client.FetchUsage(ctx, token, accountID)
	if errUsage != nil {
		log.WithError(errUsage).WithField("auth_id", authID).Warn("codex banked reset: usage fetch failed")
		return false
	}
	if !codexquota.ShouldConsumeBankedReset(usage) {
		log.WithFields(log.Fields{
			"auth_id":   authID,
			"main":      floatOrNeg(usage.MainWeeklyUsed),
			"spark":     floatOrNeg(usage.SparkWeeklyUsed),
			"credits":   intOrNeg(usage.ResetCreditsAvailable),
		}).Debug("codex banked reset skipped: weekly bars not both depleted or no credits")
		return false
	}

	windowsReset, errConsume := client.ConsumeResetCredit(ctx, token, accountID)
	if errConsume != nil {
		log.WithError(errConsume).WithField("auth_id", authID).Warn("codex banked reset: consume failed")
		return false
	}

	if _, _, errReset := m.ResetQuota(ctx, authID); errReset != nil {
		log.WithError(errReset).WithField("auth_id", authID).Warn("codex banked reset: local quota clear failed after consume")
	}
	log.WithFields(log.Fields{
		"auth_id":       authID,
		"windows_reset": windowsReset,
		"credits_left":  intOrNeg(usage.ResetCreditsAvailable) - 1,
	}).Info("codex banked reset credit consumed after dual weekly exhaustion")
	return true
}

func codexAccessCredentials(auth *Auth) (token, accountID string) {
	if auth == nil {
		return "", ""
	}
	if auth.Attributes != nil {
		token = strings.TrimSpace(auth.Attributes["api_key"])
		accountID = strings.TrimSpace(auth.Attributes["account_id"])
	}
	if auth.Metadata != nil {
		if token == "" {
			if v, ok := auth.Metadata["access_token"].(string); ok {
				token = strings.TrimSpace(v)
			}
		}
		if accountID == "" {
			if v, ok := auth.Metadata["account_id"].(string); ok {
				accountID = strings.TrimSpace(v)
			}
		}
	}
	return token, accountID
}

func floatOrNeg(v *float64) float64 {
	if v == nil {
		return -1
	}
	return *v
}

func intOrNeg(v *int) int {
	if v == nil {
		return -1
	}
	return *v
}

func newCodexQuotaHTTPClient(cfg *internalconfig.Config, auth *Auth, timeout time.Duration) *http.Client {
	client := &http.Client{}
	if timeout > 0 {
		client.Timeout = timeout
	}
	proxyURL := ""
	if auth != nil {
		proxyURL = strings.TrimSpace(auth.ProxyURL)
	}
	if proxyURL == "" && cfg != nil {
		proxyURL = strings.TrimSpace(cfg.ProxyURL)
	}
	if proxyURL == "" {
		return client
	}
	transport, _, errBuild := proxyutil.BuildHTTPTransport(proxyURL)
	if errBuild != nil {
		log.WithError(errBuild).Debug("codex banked reset: proxy transport setup failed")
		return client
	}
	client.Transport = transport
	return client
}
