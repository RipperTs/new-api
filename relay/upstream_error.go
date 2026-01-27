package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AugmentUsageLimitReachedMessage extracts optional reset metadata from upstream error bodies and
// appends it to the message for easier debugging.
//
// Expected upstream format:
// {"error":{"type":"usage_limit_reached","plan_type":"team","resets_at":1769041488,"resets_in_seconds":80141}}
func AugmentUsageLimitReachedMessage(raw []byte, errType string, msg string) string {
	if !strings.EqualFold(strings.TrimSpace(errType), "usage_limit_reached") {
		return msg
	}
	base := strings.TrimSpace(msg)
	if base == "" {
		return msg
	}
	// Avoid duplicate augmentation (e.g. retry wrappers).
	if strings.Contains(base, "resets_at=") ||
		strings.Contains(base, "resets_in_seconds=") ||
		strings.Contains(base, "恢复时间=") {
		return msg
	}

	var meta struct {
		Error struct {
			PlanType        string `json:"plan_type"`
			ResetsAt        int64  `json:"resets_at"`
			ResetsInSeconds int64  `json:"resets_in_seconds"`
		} `json:"error"`
	}
	if json.Unmarshal(bytes.TrimSpace(raw), &meta) != nil {
		return msg
	}

	parts := make([]string, 0, 4)
	if strings.TrimSpace(meta.Error.PlanType) != "" {
		parts = append(parts, "plan_type="+strings.TrimSpace(meta.Error.PlanType))
	}
	if meta.Error.ResetsInSeconds > 0 {
		parts = append(parts, fmt.Sprintf("resets_in_seconds=%d", meta.Error.ResetsInSeconds))
	}
	if meta.Error.ResetsAt > 0 {
		parts = append(parts, fmt.Sprintf("resets_at=%d", meta.Error.ResetsAt))
		// Display in local timezone for readability.
		parts = append(parts, "恢复时间="+time.Unix(meta.Error.ResetsAt, 0).In(time.Local).Format(time.RFC3339))
	}
	if len(parts) == 0 {
		return msg
	}
	return base + " (" + strings.Join(parts, ", ") + ")"
}
