package codexquota

import (
	"strings"

	"github.com/tidwall/gjson"
)

const weeklyWindowSeconds int64 = 7 * 24 * 60 * 60

// Usage is a parsed Codex /wham/usage payload focused on weekly windows and banked resets.
type Usage struct {
	PlanType             string
	MainWeeklyUsed       *float64
	SparkWeeklyUsed      *float64
	ResetCreditsAvailable *int
	Raw                  []byte
}

// ParseUsage parses ChatGPT Codex usage JSON.
func ParseUsage(body []byte) Usage {
	root := gjson.ParseBytes(body)
	usage := Usage{
		PlanType: strings.TrimSpace(root.Get("plan_type").String()),
		Raw:      append([]byte(nil), body...),
	}
	if w := weeklyUsedFromRateLimit(root.Get("rate_limit")); w != nil {
		usage.MainWeeklyUsed = w
	}
	// Prefer secondary_window under additional spark limits; fall back to any weekly window.
	for _, item := range root.Get("additional_rate_limits").Array() {
		name := item.Get("limit_name").String()
		feature := item.Get("metered_feature").String()
		if !strings.Contains(strings.ToLower(name+" "+feature), "spark") {
			continue
		}
		if w := weeklyUsedFromRateLimit(item.Get("rate_limit")); w != nil {
			usage.SparkWeeklyUsed = w
			break
		}
	}
	if credits := root.Get("rate_limit_reset_credits.available_count"); credits.Exists() {
		count := int(credits.Int())
		usage.ResetCreditsAvailable = &count
	}
	return usage
}

func weeklyUsedFromRateLimit(rateLimit gjson.Result) *float64 {
	if !rateLimit.Exists() {
		return nil
	}
	secondary := rateLimit.Get("secondary_window")
	if secondary.Exists() {
		if seconds := secondary.Get("limit_window_seconds").Int(); seconds == 0 || seconds == weeklyWindowSeconds {
			v := secondary.Get("used_percent").Float()
			return &v
		}
	}
	primary := rateLimit.Get("primary_window")
	if primary.Exists() && primary.Get("limit_window_seconds").Int() == weeklyWindowSeconds {
		v := primary.Get("used_percent").Float()
		return &v
	}
	if secondary.Exists() {
		v := secondary.Get("used_percent").Float()
		return &v
	}
	return nil
}

// ShouldConsumeBankedReset reports whether both weekly bars are depleted and credits remain.
// Spark can still run after the main weekly is empty, so both bars are required.
func ShouldConsumeBankedReset(usage Usage) bool {
	if usage.ResetCreditsAvailable == nil || *usage.ResetCreditsAvailable <= 0 {
		return false
	}
	if usage.MainWeeklyUsed == nil || usage.SparkWeeklyUsed == nil {
		return false
	}
	return *usage.MainWeeklyUsed >= 100 && *usage.SparkWeeklyUsed >= 100
}
