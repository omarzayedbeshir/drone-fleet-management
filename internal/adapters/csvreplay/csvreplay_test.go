package csvreplay

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/internal/adapters/adaptertest"
	"github.com/omarzayedbeshir/drone-fleet-management/internal/vendor"
)

const goodCSV = `device_sn,timestamp,latitude,longitude,battery_capacity_percent,fault_codes,armed
D1,1730000000000,30.1,31.2,80,,true
D2,1730000001000,30.2,31.3,79,0x0A01,false
D1,1730000002000,30.3,31.4,78,,true
D2,1730000003000,30.4,31.5,77,,false
D1,1730000004000,30.5,31.6,76,,true
`

func fromString(s string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(s)), nil }
}

func newAdapter(t *testing.T, cfg Config) *Adapter {
	t.Helper()
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func replay(t *testing.T, a *Adapter) ([]vendor.RawRecord, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan vendor.RawRecord, 1000)
	err := a.Subscribe(ctx, out)
	close(out)
	var recs []vendor.RawRecord
	for r := range out {
		recs = append(recs, r)
	}
	return recs, err
}

func TestConformance(t *testing.T) {
	adaptertest.Run(t, func(t *testing.T) vendor.VendorAdapter {
		return newAdapter(t, Config{Open: fromString(goodCSV), Loop: true})
	}, adaptertest.Options{WantSimulated: true, MinRecords: 12}) // 12 > 5 rows: exercises looping
}

func TestReplayTypesAndLabels(t *testing.T) {
	a := newAdapter(t, Config{Open: fromString(goodCSV), Vendor: "dji"})
	if err := a.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	recs, err := replay(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 5 {
		t.Fatalf("got %d records, want 5", len(recs))
	}
	r := recs[1]
	if r.Vendor != "dji" || a.Name() != "csv" {
		t.Errorf("vendor/name = %q/%q, want dji/csv", r.Vendor, a.Name())
	}
	if !r.Simulated {
		t.Error("replayed data must be labelled simulated")
	}
	if r.SourceDroneID != "D2" {
		t.Errorf("drone id %q", r.SourceDroneID)
	}
	if r.Payload["latitude"] != 30.2 || r.Payload["armed"] != false || r.Payload["fault_codes"] != "0x0A01" {
		t.Errorf("bad payload types: %#v", r.Payload)
	}
	if _, present := recs[0].Payload["fault_codes"]; present {
		t.Error("empty cell must be omitted")
	}
	if got := a.Stats(); got.Emitted != 5 || got.Skipped != 0 {
		t.Errorf("stats = %+v", got)
	}
}

func TestBadRowsAreSkippedAndCounted(t *testing.T) {
	data := "device_sn,timestamp,latitude\n" +
		"D1,1730000000000,30.1\n" +
		"D1,1730000001000\n" + // too few fields
		"D1,1730000002000,30.1,extra\n" + // too many
		",1730000003000,30.2\n" + // no drone id
		"D1,\"unterminated,30.3\n" + // bad quoting swallows rest of file
		""
	a := newAdapter(t, Config{Open: fromString(data)})
	recs, err := replay(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if a.Stats().Skipped < 3 {
		t.Errorf("skipped = %d, want >= 3", a.Stats().Skipped)
	}
}

func TestBOMAndWhitespaceHeader(t *testing.T) {
	a := newAdapter(t, Config{Open: fromString("\ufeffdevice_sn , latitude\nD1,1\n")})
	recs, err := replay(t, a)
	if err != nil || len(recs) != 1 || recs[0].Payload["latitude"] != 1.0 {
		t.Fatalf("recs=%v err=%v", recs, err)
	}
}

func TestDroneIDKeptAsText(t *testing.T) {
	a := newAdapter(t, Config{Open: fromString("device_sn,x\n007,1\n")})
	recs, _ := replay(t, a)
	if recs[0].SourceDroneID != "007" || recs[0].Payload["device_sn"] != "007" {
		t.Fatalf("id mangled: %#v", recs[0])
	}
}

func TestPermanentErrors(t *testing.T) {
	cases := map[string]Config{
		"no id column":     {Open: fromString("a,b\n1,2\n")},
		"duplicate header": {Open: fromString("device_sn,device_sn\n1,2\n")},
		"empty header":     {Open: fromString("device_sn,\n1,2\n")},
		"empty file":       {Open: fromString("")},
		"missing file":     {Path: filepath.Join(t.TempDir(), "nope.csv")},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			a := newAdapter(t, cfg)
			err := a.Connect(context.Background())
			if !errors.Is(err, vendor.ErrPermanent) {
				t.Fatalf("Connect err = %v, want ErrPermanent", err)
			}
			if a.Health().State != vendor.HealthDown {
				t.Errorf("health = %v", a.Health().State)
			}
			if _, err := replay(t, a); !errors.Is(err, vendor.ErrPermanent) {
				t.Errorf("Subscribe err = %v, want ErrPermanent", err)
			}
		})
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, vendor.ErrPermanent) {
		t.Errorf("empty config: %v", err)
	}
	if _, err := New(Config{Path: "x", Speed: -1}); !errors.Is(err, vendor.ErrPermanent) {
		t.Errorf("negative speed: %v", err)
	}
}

func TestReadsRealFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.csv")
	if err := os.WriteFile(path, []byte(goodCSV), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newAdapter(t, Config{Path: path})
	recs, err := replay(t, a)
	if err != nil || len(recs) != 5 {
		t.Fatalf("recs=%d err=%v", len(recs), err)
	}
}

func TestLoopWithNoUsableRowsFailsInsteadOfSpinning(t *testing.T) {
	a := newAdapter(t, Config{Open: fromString("device_sn,x\n"), Loop: true})
	if _, err := replay(t, a); !errors.Is(err, vendor.ErrPermanent) {
		t.Fatalf("err = %v", err)
	}
}

func TestPacing(t *testing.T) {
	// Samples 1 s apart in the recording; at 100x that is ~10 ms per gap.
	a := newAdapter(t, Config{Open: fromString(goodCSV), Speed: 100})
	start := time.Now()
	recs, err := replay(t, a)
	el := time.Since(start)
	if err != nil || len(recs) != 5 {
		t.Fatalf("recs=%d err=%v", len(recs), err)
	}
	if el < 35*time.Millisecond || el > 2*time.Second {
		t.Fatalf("4 gaps of ~10ms expected, replay took %v", el)
	}

	// MaxDelay caps long gaps in the recording.
	gap := "device_sn,timestamp\nD1,1730000000000\nD1,1730003600000\n" // 1 hour apart
	b := newAdapter(t, Config{Open: fromString(gap), Speed: 1, MaxDelay: 20 * time.Millisecond})
	start = time.Now()
	if _, err := replay(t, b); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("MaxDelay not applied")
	}
}

func TestCancelDuringPacing(t *testing.T) {
	gap := "device_sn,timestamp\nD1,1730000000000\nD1,1730003600000\n"
	a := newAdapter(t, Config{Open: fromString(gap), Speed: 1, MaxDelay: time.Hour})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	out := make(chan vendor.RawRecord, 10)
	start := time.Now()
	if err := a.Subscribe(ctx, out); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("did not return promptly on cancel")
	}
}

// The committed sample is the R-02 fallback: it must always replay cleanly.
func TestCommittedDJISampleReplays(t *testing.T) {
	a := newAdapter(t, Config{Path: "../../../testdata/dji_sample.csv", Vendor: "dji"})
	if err := a.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	recs, err := replay(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 120 || a.Stats().Skipped != 0 {
		t.Fatalf("records=%d skipped=%d, want 120/0", len(recs), a.Stats().Skipped)
	}
	ids := map[string]bool{}
	for _, r := range recs {
		ids[r.SourceDroneID] = true
		if r.Vendor != "dji" || !r.Simulated {
			t.Fatalf("bad envelope %+v", r)
		}
	}
	if len(ids) != 3 {
		t.Fatalf("drones = %v", ids)
	}
}
