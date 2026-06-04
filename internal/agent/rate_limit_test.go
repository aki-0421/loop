package agent

import (
	"testing"
	"time"
)

func TestDetectRateLimitCodexTryAgainDuration(t *testing.T) {
	now := time.Date(2026, time.June, 5, 10, 0, 0, 0, time.UTC)
	err, ok := DetectRateLimit("Rate limit reached for o3 in organization org-REDACTED on tokens per min (TPM): Limit 30000, Used 23669, Requested 29142. Please try again in 45.622s.", now)
	if !ok {
		t.Fatal("expected Codex rate limit message to be detected")
	}
	if got, want := err.RetryAfter, 45622*time.Millisecond; got != want {
		t.Fatalf("retry after = %s, want %s", got, want)
	}
}

func TestDetectRateLimitCodexExceededRetryLimit(t *testing.T) {
	now := time.Date(2026, time.June, 5, 10, 0, 0, 0, time.UTC)
	err, ok := DetectRateLimit("exceeded retry limit, last status: 429 Too Many Requests, request id: 9c858ed14adedcfe-SJC", now)
	if !ok {
		t.Fatal("expected Codex exceeded retry limit message to be detected")
	}
	if got := err.WaitDuration(now); got != defaultRateLimitRetryAfter {
		t.Fatalf("wait duration = %s, want fallback %s", got, defaultRateLimitRetryAfter)
	}
}

func TestDetectRateLimitCodexRateLimitsReadResetsAt(t *testing.T) {
	now := time.Date(2026, time.May, 22, 12, 0, 0, 0, time.UTC)
	text := `{"type":"event_msg","payload":{"type":"token_count","info":{"rate_limits":{"limitId":"codex","primary":{"usedPercent":100,"windowDurationMins":300,"resetsAt":1779459394},"secondary":{"usedPercent":18,"windowDurationMins":10080,"resetsAt":1779826837},"rateLimitReachedType":"primary"}}}}
exceeded retry limit, last status: 429 Too Many Requests`
	err, ok := DetectRateLimit(text, now)
	if !ok {
		t.Fatal("expected Codex rate_limits payload to be detected")
	}
	want := time.Unix(1779459394, 0).UTC()
	if !err.ResetAt.Equal(want) {
		t.Fatalf("reset at = %s, want %s", err.ResetAt, want)
	}
}

func TestDetectRateLimitCodexRateLimitsReadSecondaryReset(t *testing.T) {
	now := time.Date(2026, time.May, 22, 12, 0, 0, 0, time.UTC)
	text := `{"rateLimits":{"primary":{"resetsAt":1779459394},"secondary":{"resetsAt":1779826837},"rateLimitReachedType":"secondary"}}
exceeded retry limit, last status: 429 Too Many Requests`
	err, ok := DetectRateLimit(text, now)
	if !ok {
		t.Fatal("expected Codex secondary rate limit payload to be detected")
	}
	want := time.Unix(1779826837, 0).UTC()
	if !err.ResetAt.Equal(want) {
		t.Fatalf("reset at = %s, want %s", err.ResetAt, want)
	}
}

func TestDetectRateLimitCodexUsageLimitResetClock(t *testing.T) {
	loc := time.FixedZone("test", 9*60*60)
	now := time.Date(2026, time.June, 5, 13, 0, 0, 0, loc)
	err, ok := DetectRateLimit("You've hit your usage limit. 5h limit: 96% left (resets 13:37)", now)
	if !ok {
		t.Fatal("expected Codex usage limit message to be detected")
	}
	want := time.Date(2026, time.June, 5, 13, 37, 0, 0, loc)
	if !err.ResetAt.Equal(want) {
		t.Fatalf("reset at = %s, want %s", err.ResetAt, want)
	}
}

func TestDetectRateLimitCodexResetDate(t *testing.T) {
	loc := time.FixedZone("test", -7*60*60)
	now := time.Date(2025, time.September, 25, 12, 0, 0, 0, loc)
	err, ok := DetectRateLimit("Resets at: Sep 25, 2025 2:37 PM. Visit https://platform.openai.com/account/rate-limits to learn more.", now)
	if !ok {
		t.Fatal("expected Codex reset date message to be detected")
	}
	want := time.Date(2025, time.September, 25, 14, 37, 0, 0, loc)
	if !err.ResetAt.Equal(want) {
		t.Fatalf("reset at = %s, want %s", err.ResetAt, want)
	}
}

func TestDetectRateLimitClaudeSessionLimit(t *testing.T) {
	loc := time.FixedZone("test", -5*60*60)
	now := time.Date(2026, time.June, 5, 15, 0, 0, 0, loc)
	err, ok := DetectRateLimit("You've hit your session limit · resets 3:45pm", now)
	if !ok {
		t.Fatal("expected Claude Code session limit message to be detected")
	}
	want := time.Date(2026, time.June, 5, 15, 45, 0, 0, loc)
	if !err.ResetAt.Equal(want) {
		t.Fatalf("reset at = %s, want %s", err.ResetAt, want)
	}
}

func TestDetectRateLimitClaudeWeeklyLimit(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, time.June, 5, 15, 0, 0, 0, loc)
	err, ok := DetectRateLimit("You've hit your weekly limit · resets Mon 12:00am", now)
	if !ok {
		t.Fatal("expected Claude Code weekly limit message to be detected")
	}
	want := time.Date(2026, time.June, 8, 0, 0, 0, 0, loc)
	if !err.ResetAt.Equal(want) {
		t.Fatalf("reset at = %s, want %s", err.ResetAt, want)
	}
}

func TestDetectRateLimitDoesNotTreatHardStopsAsWaitable(t *testing.T) {
	now := time.Date(2026, time.June, 5, 10, 0, 0, 0, time.UTC)
	for _, text := range []string{
		"Credit balance is too low",
		"Request too large (max 30 MB)",
		"Not logged in · Please run /login",
	} {
		if _, ok := DetectRateLimit(text, now); ok {
			t.Fatalf("did not expect %q to be detected as waitable rate limit", text)
		}
	}
}
