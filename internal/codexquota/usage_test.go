package codexquota

import "testing"

func TestParseUsageAndShouldConsume(t *testing.T) {
	body := []byte(`{
		"plan_type":"plus",
		"rate_limit":{
			"secondary_window":{"used_percent":100,"limit_window_seconds":604800}
		},
		"additional_rate_limits":[{
			"limit_name":"GPT-5.3-Codex-Spark",
			"rate_limit":{"secondary_window":{"used_percent":100,"limit_window_seconds":604800}}
		}],
		"rate_limit_reset_credits":{"available_count":2}
	}`)
	usage := ParseUsage(body)
	if usage.MainWeeklyUsed == nil || *usage.MainWeeklyUsed != 100 {
		t.Fatalf("main weekly = %#v", usage.MainWeeklyUsed)
	}
	if usage.SparkWeeklyUsed == nil || *usage.SparkWeeklyUsed != 100 {
		t.Fatalf("spark weekly = %#v", usage.SparkWeeklyUsed)
	}
	if usage.ResetCreditsAvailable == nil || *usage.ResetCreditsAvailable != 2 {
		t.Fatalf("credits = %#v", usage.ResetCreditsAvailable)
	}
	if !ShouldConsumeBankedReset(usage) {
		t.Fatal("expected should consume when both weekly bars depleted")
	}

	// Spark still has room — do not burn a banked reset.
	body2 := []byte(`{
		"rate_limit":{"secondary_window":{"used_percent":100,"limit_window_seconds":604800}},
		"additional_rate_limits":[{"limit_name":"GPT-5.3-Codex-Spark","rate_limit":{"secondary_window":{"used_percent":40,"limit_window_seconds":604800}}}],
		"rate_limit_reset_credits":{"available_count":1}
	}`)
	if ShouldConsumeBankedReset(ParseUsage(body2)) {
		t.Fatal("expected no consume when spark weekly remains")
	}
}
