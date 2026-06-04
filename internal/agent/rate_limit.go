package agent

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const defaultRateLimitRetryAfter = 5 * time.Minute

var (
	rateLimitRFC3339Pattern      = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})\b`)
	rateLimitRetryAfterPattern   = regexp.MustCompile(`(?i)\bretry-after\b\s*[:=]?\s*(\d+(?:\.\d+)?)\s*(milliseconds?|msecs?|ms|seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h)?\b`)
	rateLimitTryAgainPattern     = regexp.MustCompile(`(?i)\b(?:please\s+)?(?:try again|retry|retrying|wait)\b.{0,32}?\bin\s+((?:\d+(?:\.\d+)?\s*(?:milliseconds?|msecs?|ms|seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h)\s*)+)`)
	rateLimitResetClockPattern   = regexp.MustCompile(`(?i)\bresets?\s+(?:at\s+)?(?:(sun|mon|tue|wed|thu|fri|sat)(?:day)?\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?(?:\s+on\s+(\d{1,2})\s+([a-z]{3,9}))?\b`)
	rateLimitUntilClockPattern   = regexp.MustCompile(`(?i)\buntil\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b`)
	rateLimitDurationPartPattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(milliseconds?|msecs?|ms|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b`)
)

type RateLimitError struct {
	Message    string
	RetryAfter time.Duration
	ResetAt    time.Time
}

func (e *RateLimitError) Error() string {
	if e == nil {
		return "agent rate limit reached"
	}
	if !e.ResetAt.IsZero() {
		return fmt.Sprintf("agent rate limit reached; reset at %s", e.ResetAt.Format(time.RFC3339))
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("agent rate limit reached; retry after %s", e.RetryAfter.Round(time.Second))
	}
	return "agent rate limit reached"
}

func (e *RateLimitError) WaitDuration(now time.Time) time.Duration {
	if e == nil {
		return 0
	}
	if !e.ResetAt.IsZero() {
		wait := time.Until(e.ResetAt)
		if !now.IsZero() {
			wait = e.ResetAt.Sub(now)
		}
		if wait > 0 {
			return wait
		}
	}
	if e.RetryAfter > 0 {
		return e.RetryAfter
	}
	return defaultRateLimitRetryAfter
}

func IsRateLimit(err error) bool {
	var rateLimit *RateLimitError
	return errors.As(err, &rateLimit)
}

func RateLimitFromError(err error) (*RateLimitError, bool) {
	var rateLimit *RateLimitError
	if errors.As(err, &rateLimit) {
		return rateLimit, true
	}
	return nil, false
}

func DetectRateLimit(text string, now time.Time) (*RateLimitError, bool) {
	text = strings.TrimSpace(text)
	if text == "" || !looksLikeRateLimit(text) {
		return nil, false
	}
	err := &RateLimitError{Message: compactRateLimitMessage(text)}
	if resetAt, ok := parseRateLimitRFC3339(text); ok {
		err.ResetAt = resetAt
		return err, true
	}
	if resetAt, ok := parseRateLimitClock(text, now); ok {
		err.ResetAt = resetAt
		return err, true
	}
	if retryAfter, ok := parseRateLimitDuration(text); ok {
		err.RetryAfter = retryAfter
		return err, true
	}
	err.RetryAfter = defaultRateLimitRetryAfter
	return err, true
}

func looksLikeRateLimit(text string) bool {
	lower := strings.ToLower(text)
	for _, hardStop := range []string{
		"request too large",
		"image was too large",
		"credit balance is too low",
		"invalid api key",
		"not logged in",
	} {
		if strings.Contains(lower, hardStop) {
			return false
		}
	}
	for _, marker := range []string{
		"rate_limit_exceeded",
		"rate_limit_reached",
		"rate-limit exceeded",
		"rate-limit reached",
		"rate_limit exceeded",
		"rate_limit reached",
		"rate limit reached",
		"rate limit exceeded",
		"you've hit your usage limit",
		"you have hit your usage limit",
		"you've hit your session limit",
		"you have hit your session limit",
		"you've hit your weekly limit",
		"you have hit your weekly limit",
		"you've hit your opus limit",
		"you have hit your opus limit",
		"session limit",
		"weekly limit",
		"opus limit",
		"server is temporarily limiting requests",
		"limit reached",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if strings.Contains(lower, "429") && (strings.Contains(lower, "too many requests") || strings.Contains(lower, "request rejected") || strings.Contains(lower, "exceeded retry limit")) {
		return true
	}
	return false
}

func parseRateLimitRFC3339(text string) (time.Time, bool) {
	for _, match := range rateLimitRFC3339Pattern.FindAllString(text, -1) {
		at, err := time.Parse(time.RFC3339, match)
		if err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

func parseRateLimitDuration(text string) (time.Duration, bool) {
	matches := rateLimitRetryAfterPattern.FindAllStringSubmatch(text, -1)
	if len(matches) > 0 {
		for _, match := range matches {
			if len(match) < 3 {
				continue
			}
			d, ok := durationFromParts(match[1], firstNonEmptyString(match[2], "seconds"))
			if ok {
				return d, true
			}
		}
	}
	matches = rateLimitTryAgainPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return 0, false
	}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		if d, ok := parseDurationParts(match[1]); ok {
			return d, true
		}
	}
	return 0, false
}

func parseDurationParts(text string) (time.Duration, bool) {
	matches := rateLimitDurationPartPattern.FindAllStringSubmatch(text, -1)
	var total time.Duration
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		if d, ok := durationFromParts(match[1], match[2]); ok {
			total += d
		}
	}
	return total, total > 0
}

func durationFromParts(value, unit string) (time.Duration, bool) {
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	switch strings.ToLower(unit) {
	case "millisecond", "milliseconds", "msec", "msecs", "ms":
		return time.Duration(n * float64(time.Millisecond)), true
	case "hour", "hours", "hr", "hrs", "h":
		return time.Duration(n * float64(time.Hour)), true
	case "minute", "minutes", "min", "mins", "m":
		return time.Duration(n * float64(time.Minute)), true
	case "second", "seconds", "sec", "secs", "s":
		return time.Duration(n * float64(time.Second)), true
	default:
		return 0, false
	}
}

func parseRateLimitClock(text string, now time.Time) (time.Time, bool) {
	if now.IsZero() {
		now = time.Now()
	}
	match := rateLimitResetClockPattern.FindStringSubmatch(text)
	if len(match) > 0 {
		return clockMatchToTime(match[2], match[3], match[4], match[1], match[5], match[6], now)
	}
	match = rateLimitUntilClockPattern.FindStringSubmatch(text)
	if len(match) > 0 {
		return clockMatchToTime(match[1], match[2], match[3], "", "", "", now)
	}
	return time.Time{}, false
}

func clockMatchToTime(hourText, minuteText, meridian, weekday, dayText, monthText string, now time.Time) (time.Time, bool) {
	hour, err := strconv.Atoi(hourText)
	if err != nil {
		return time.Time{}, false
	}
	minute := 0
	if minuteText != "" {
		minute, err = strconv.Atoi(minuteText)
		if err != nil {
			return time.Time{}, false
		}
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return time.Time{}, false
	}
	meridian = strings.ToLower(meridian)
	if meridian != "" {
		if hour < 1 || hour > 12 {
			return time.Time{}, false
		}
		if meridian == "pm" && hour != 12 {
			hour += 12
		}
		if meridian == "am" && hour == 12 {
			hour = 0
		}
	}
	resetAt := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if month, ok := monthByName(monthText); ok && dayText != "" {
		day, err := strconv.Atoi(dayText)
		if err != nil || day < 1 || day > 31 {
			return time.Time{}, false
		}
		resetAt = time.Date(now.Year(), month, day, hour, minute, 0, 0, now.Location())
		if !resetAt.After(now) {
			resetAt = time.Date(now.Year()+1, month, day, hour, minute, 0, 0, now.Location())
		}
		return resetAt, true
	}
	if day, ok := weekdayByName(weekday); ok {
		for resetAt.Weekday() != day || !resetAt.After(now) {
			resetAt = resetAt.Add(24 * time.Hour)
		}
		return resetAt, true
	}
	if !resetAt.After(now) {
		resetAt = resetAt.Add(24 * time.Hour)
	}
	return resetAt, true
}

func weekdayByName(name string) (time.Weekday, bool) {
	switch strings.ToLower(name) {
	case "sun", "sunday":
		return time.Sunday, true
	case "mon", "monday":
		return time.Monday, true
	case "tue", "tuesday":
		return time.Tuesday, true
	case "wed", "wednesday":
		return time.Wednesday, true
	case "thu", "thursday":
		return time.Thursday, true
	case "fri", "friday":
		return time.Friday, true
	case "sat", "saturday":
		return time.Saturday, true
	default:
		return time.Sunday, false
	}
}

func monthByName(name string) (time.Month, bool) {
	switch strings.ToLower(name) {
	case "jan", "january":
		return time.January, true
	case "feb", "february":
		return time.February, true
	case "mar", "march":
		return time.March, true
	case "apr", "april":
		return time.April, true
	case "may":
		return time.May, true
	case "jun", "june":
		return time.June, true
	case "jul", "july":
		return time.July, true
	case "aug", "august":
		return time.August, true
	case "sep", "sept", "september":
		return time.September, true
	case "oct", "october":
		return time.October, true
	case "nov", "november":
		return time.November, true
	case "dec", "december":
		return time.December, true
	default:
		return 0, false
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func compactRateLimitMessage(text string) string {
	fields := strings.Fields(text)
	if len(fields) <= 32 {
		return strings.Join(fields, " ")
	}
	return strings.Join(fields[:32], " ") + "..."
}
