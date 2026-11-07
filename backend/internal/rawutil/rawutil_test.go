package rawutil

import (
	"reflect"
	"testing"
	"time"
)

func TestFlatten(t *testing.T) {
	in := map[string]any{
		"lat":     30.0,
		"battery": map[string]any{"percent": 80.0, "temp": map[string]any{"c": 31.0}},
		"faults":  []any{"0x1", "0x2"},
		"gone":    nil,
		"empty":   []any{},
	}
	want := map[string]any{
		"lat":             30.0,
		"battery_percent": 80.0,
		"battery_temp_c":  31.0,
		"faults":          "0x1;0x2",
	}
	if got := Flatten(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("Flatten = %#v, want %#v", got, want)
	}
}

func TestInferValue(t *testing.T) {
	cases := []struct {
		in   string
		want any
		ok   bool
	}{
		{"12.5", 12.5, true},
		{" 7 ", 7.0, true},
		{"TRUE", true, true},
		{"false", false, true},
		{"abc", "abc", true},
		{"", nil, false},
		{"   ", nil, false},
	}
	for _, c := range cases {
		got, ok := InferValue(c.in)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("InferValue(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestParseTimestamp(t *testing.T) {
	want := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for name, in := range map[string]any{
		"rfc3339": "2026-10-20T12:00:00Z",
		"seconds": float64(want.Unix()),
		"millis":  float64(want.UnixMilli()),
	} {
		got, ok := ParseTimestamp(in)
		if !ok || !got.Equal(want) {
			t.Errorf("%s: got %v,%v want %v", name, got, ok, want)
		}
	}
	for _, bad := range []any{"yesterday", -5.0, 0.0, nil, true} {
		if _, ok := ParseTimestamp(bad); ok {
			t.Errorf("ParseTimestamp(%v) should fail", bad)
		}
	}
}
