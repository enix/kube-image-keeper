package api

import (
	"encoding/json"
	"time"
)

// LogEntry is one logged registry request (GET /requests).
// User "" is anonymous. Kind "" means the request was rejected before
// classification. Status 0 means no HTTP response (reset, or client gone
// during latency).
type LogEntry struct {
	Time      time.Time     `json:"time"`
	Host      string        `json:"host"`
	ClientIP  string        `json:"clientIp"`
	User      string        `json:"user"`
	UserAgent string        `json:"userAgent"`
	Caller    string        `json:"caller"`
	Method    string        `json:"method"`
	Kind      Kind          `json:"kind"`
	Repo      string        `json:"repo"`
	Reference string        `json:"reference"`
	Status    int           `json:"status"`
	Digest    string        `json:"digest"`
	Duration  time.Duration `json:"-"`
	RuleID    string        `json:"ruleId"`
	Forwarded bool          `json:"forwarded"`
	// UnknownHost marks a request for a host outside the base domain; Host
	// then holds the raw Host header and Stats counts it under "(unknown)".
	UnknownHost bool `json:"unknownHost,omitempty"`
}

// MarshalJSON renders Duration as durationMs, a float in milliseconds.
func (e LogEntry) MarshalJSON() ([]byte, error) {
	type plain LogEntry
	return json.Marshal(struct {
		plain
		DurationMs float64 `json:"durationMs"`
	}{plain(e), float64(e.Duration) / float64(time.Millisecond)})
}

// UnmarshalJSON reads durationMs back into Duration, so a client sees the
// duration the admin measured.
func (e *LogEntry) UnmarshalJSON(b []byte) error {
	type plain LogEntry
	aux := struct {
		*plain
		DurationMs float64 `json:"durationMs"`
	}{plain: (*plain)(e)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	e.Duration = time.Duration(aux.DurationMs * float64(time.Millisecond))
	return nil
}

// Stat is a cumulative request counter (GET /stats).
type Stat struct {
	Host   string `json:"host"`
	Kind   Kind   `json:"kind"`
	Method string `json:"method"`
	Status int    `json:"status"`
	Count  int64  `json:"count"`
}
