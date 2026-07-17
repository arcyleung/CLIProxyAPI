package management

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/codexquota"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// GetCodexUsage fetches ChatGPT Codex /wham/usage for one auth index.
// GET /v0/management/codex-usage?auth_index=...
func (h *Handler) GetCodexUsage(c *gin.Context) {
	auth, ok := h.codexAuthFromRequest(c)
	if !ok {
		return
	}
	token, accountID := codexAccessCredentials(auth)
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "codex access token not found"})
		return
	}
	client := &codexquota.Client{
		HTTP: helps.NewProxyAwareHTTPClient(c.Request.Context(), h.cfg, auth, 20*time.Second),
	}
	usage, err := client.FetchUsage(c.Request.Context(), token, accountID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"auth_index":                  auth.Index,
		"plan_type":                   usage.PlanType,
		"main_weekly_used_percent":    usage.MainWeeklyUsed,
		"spark_weekly_used_percent":   usage.SparkWeeklyUsed,
		"reset_credits_available":     usage.ResetCreditsAvailable,
		"should_consume_banked_reset": codexquota.ShouldConsumeBankedReset(usage),
		"auto_use_banked_reset":          coreauth.AuthAutoUseBankedResetEnabled(auth),
		"raw":                         string(usage.Raw),
	})
}

// PutCodexAutoUseBankedReset sets per-account automatic banked-reset preference.
// PUT /v0/management/codex-auto-use-banked-reset
// Body: {"auth_index":"...","enabled":true}
func (h *Handler) PutCodexAutoUseBankedReset(c *gin.Context) {
	if h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}
	var req struct {
		AuthIndex string `json:"auth_index"`
		Enabled   *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled is required"})
		return
	}
	auth := h.authByIndex(strings.TrimSpace(req.AuthIndex))
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth is not a codex credential"})
		return
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}
	auth.Metadata["auto_use_banked_reset"] = *req.Enabled
	if *req.Enabled {
		auth.Attributes["auto_use_banked_reset"] = "true"
	} else {
		auth.Attributes["auto_use_banked_reset"] = "false"
	}
	auth.UpdatedAt = time.Now()
	if _, err := h.authManager.Update(c.Request.Context(), auth); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to update auth: %v", err)})
		return
	}
	auth.EnsureIndex()
	c.JSON(http.StatusOK, gin.H{
		"status":             "ok",
		"auth_index":         auth.Index,
		"auto_use_banked_reset": *req.Enabled,
	})
}

// ConsumeCodexBankedReset spends one banked reset credit for a Codex auth.
// POST /v0/management/codex-banked-reset
// Body: {"auth_index":"...","force":false}
// When force is false (default), consume only if both weekly bars are depleted.
func (h *Handler) ConsumeCodexBankedReset(c *gin.Context) {
	if h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}
	var req struct {
		AuthIndex string `json:"auth_index"`
		Force     bool   `json:"force"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	authIndex := strings.TrimSpace(req.AuthIndex)
	if authIndex == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth_index is required"})
		return
	}
	auth := h.authByIndex(authIndex)
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth is not a codex credential"})
		return
	}
	auth.EnsureIndex()

	token, accountID := codexAccessCredentials(auth)
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "codex access token not found"})
		return
	}
	client := &codexquota.Client{
		HTTP: helps.NewProxyAwareHTTPClient(c.Request.Context(), h.cfg, auth, 20*time.Second),
	}
	usage, errUsage := client.FetchUsage(c.Request.Context(), token, accountID)
	if errUsage != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": errUsage.Error()})
		return
	}
	if !req.Force && !codexquota.ShouldConsumeBankedReset(usage) {
		c.JSON(http.StatusConflict, gin.H{
			"error":                     "banked reset not eligible",
			"main_weekly_used_percent":  usage.MainWeeklyUsed,
			"spark_weekly_used_percent": usage.SparkWeeklyUsed,
			"reset_credits_available":   usage.ResetCreditsAvailable,
			"hint":                      "both weekly bars must be depleted unless force=true",
		})
		return
	}
	if usage.ResetCreditsAvailable == nil || *usage.ResetCreditsAvailable <= 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "no banked reset credits available"})
		return
	}
	windowsReset, errConsume := client.ConsumeResetCredit(c.Request.Context(), token, accountID)
	if errConsume != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": errConsume.Error()})
		return
	}
	if _, _, errReset := h.authManager.ResetQuota(c.Request.Context(), auth.ID); errReset != nil {
		c.JSON(http.StatusOK, gin.H{
			"status":        "ok",
			"windows_reset": windowsReset,
			"auth_index":    auth.Index,
			"warning":       fmt.Sprintf("credit consumed but local quota clear failed: %v", errReset),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":        "ok",
		"windows_reset": windowsReset,
		"auth_index":    auth.Index,
	})
}

func (h *Handler) codexAuthFromRequest(c *gin.Context) (*coreauth.Auth, bool) {
	if h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return nil, false
	}
	authIndex := strings.TrimSpace(c.Query("auth_index"))
	if authIndex == "" {
		authIndex = strings.TrimSpace(c.Query("authIndex"))
	}
	if authIndex == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth_index is required"})
		return nil, false
	}
	auth := h.authByIndex(authIndex)
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return nil, false
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth is not a codex credential"})
		return nil, false
	}
	auth.EnsureIndex()
	return auth, true
}

func codexAccessCredentials(auth *coreauth.Auth) (token, accountID string) {
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
