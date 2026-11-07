// Package rawutil holds small helpers shared by the Sprint 1 adapters so that
// the live DJI adapter, the CSV replay adapter and the mock feed all emit the
// SAME raw record shape. That is what lets a recorded DJI sample replayed
// through the CSV adapter stand in for live DJI (R-02) with one normalizer mapping.
package rawutil

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Flatten turns nested JSON-style objects into a single-level map. Nested keys
// are joined with "_": {"battery":{"capacity_percent":80}} becomes
// {"battery_capacity_percent":80}. Arrays of scalars are joined with ";" so a
// CSV cell and a JSON array carry the same text. nil values and empty arrays are dropped.
func Flatten(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	flattenInto(out, "", in)
	return out
}

func flattenInto(out map[string]any, prefix string, in map[string]any) {
	for k, v := range in {
		key := k
		if prefix != "" {
			key = prefix + "_" + k
		}
		switch t := v.(type) {
		case nil:
			// dropped
		case map[string]any:
			flattenInto(out, key, t)
		case []any:
			if len(t) == 0 {
				continue // an empty list and an empty CSV cell mean the same: no value
			}
			parts := make([]string, 0, len(t))
			for _, e := range t {
				switch e.(type) {
				case map[string]any, []any:
					continue // never stringify arbitrary nested data into telemetry
				}
				parts = append(parts, fmt.Sprint(e))
			}
			out[key] = strings.Join(parts, ";")
		default:
			out[key] = v
		}
	}
}

// InferValue converts a CSV cell to float64, bool or string. Empty cells
// return (nil, false) so callers can omit the field.
func InferValue(cell string) (any, bool) {
	s := strings.TrimSpace(cell)
	if s == "" {
		return nil, false
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, true
	}
	switch strings.ToLower(s) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return s, true
}

// ParseTimestamp understands RFC 3339, epoch seconds and epoch milliseconds
// (values above 1e11 are treated as milliseconds). It exists so the replay
// adapter can pace a recording; the real timestamp conversion belongs to the
// normalization stage.
func ParseTimestamp(v any) (time.Time, bool) {
	switch t := v.(type) {
	case float64:
		if t <= 0 {
			return time.Time{}, false
		}
		if t > 1e11 {
			return time.UnixMilli(int64(t)).UTC(), true
		}
		return time.Unix(int64(t), 0).UTC(), true
	case string:
		if ts, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

// SortedKeys returns the keys of m in order (deterministic output for tests).
func SortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
