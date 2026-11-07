package telemetry

import (
	"strings"
	"testing"
)

func TestCycleReadingValidation(t *testing.T) {
	for _, total := range []int64{0, 207, MaxManualCycles} {
		r := CycleReading{Total: total, Reason: "  Service log ABC-123  "}
		if err := r.Validate(); err != nil || r.Reason != "Service log ABC-123" {
			t.Fatalf("%+v %v", r, err)
		}
	}
	for _, r := range []CycleReading{{Total: -1, Reason: "Known log"}, {Total: MaxManualCycles + 1, Reason: "Known log"}, {ExpectedTotal: -1, Reason: "Known log"}, {ExpectedRevision: -1, Reason: "Known log"}, {Reason: "tiny"}, {Reason: strings.Repeat("x", 501)}, {Reason: "log\x00ref"}} {
		if r.Validate() == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
}
