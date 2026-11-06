// Package csvreplay is the CSV / replay adapter (section 4.2, R-02, R-09).
//
// It replays a recorded flight log as if it were a live vendor. It is the
// fallback when DJI access is delayed, the demo source, and a test source. It
// is NOT a supported vendor. Everything it emits is marked Simulated so the
// demo can say so (NFR-08).
//
// File format: a header row, then one telemetry sample per row. Column names
// are vendor-native and are passed through untouched (empty cells are
// omitted, numbers become float64, true/false become bool, everything else
// stays a string). Set Config.Vendor to the vendor the recording came from
// (for example "dji") so the normalizer applies that vendor's field mapping.
package csvreplay

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/internal/rawutil"
	"github.com/omarzayedbeshir/drone-fleet-management/internal/vendor"
)

// Config configures a replay adapter.
type Config struct {
	// Name is the adapter name. Default "csv".
	Name string
	// Vendor is the vendor the recording imitates; used as RawRecord.Vendor.
	// Default: Name.
	Vendor string
	// Path of the CSV file. Ignored if Open is set.
	Path string
	// Open overrides Path (embedded data, tests).
	Open func() (io.ReadCloser, error)
	// DroneIDColumn identifies the drone in each row. Default "device_sn".
	DroneIDColumn string
	// TimestampColumn is used only to pace the replay. Default "timestamp".
	TimestampColumn string
	// Speed paces the replay relative to the recording: 1 = real time,
	// 10 = ten times faster, 0 = no delay (emit as fast as consumed).
	Speed float64
	// MaxDelay caps any single pacing wait (gaps in the recording). Default 10s.
	MaxDelay time.Duration
	// Loop restarts the file at EOF instead of finishing (long demos).
	Loop bool
	// Now is the clock for ReceivedAt. Default time.Now.
	Now func() time.Time
}

// Stats counts what the adapter did with the file.
type Stats struct {
	Emitted int64 // rows turned into records
	Skipped int64 // rows that could not be used (ragged, unparsable, no drone id)
}

// Adapter implements vendor.VendorAdapter.
type Adapter struct {
	cfg Config

	emitted, skipped atomic.Int64

	mu     sync.Mutex
	health vendor.Health
}

// New validates cfg and builds an Adapter.
func New(cfg Config) (*Adapter, error) {
	if cfg.Open == nil && cfg.Path == "" {
		return nil, fmt.Errorf("csvreplay: Path or Open is required: %w", vendor.ErrPermanent)
	}
	if cfg.Name == "" {
		cfg.Name = "csv"
	}
	if cfg.Vendor == "" {
		cfg.Vendor = cfg.Name
	}
	if cfg.DroneIDColumn == "" {
		cfg.DroneIDColumn = "device_sn"
	}
	if cfg.TimestampColumn == "" {
		cfg.TimestampColumn = "timestamp"
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 10 * time.Second
	}
	if cfg.Speed < 0 {
		return nil, fmt.Errorf("csvreplay: Speed must be >= 0: %w", vendor.ErrPermanent)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Open == nil {
		path := cfg.Path
		cfg.Open = func() (io.ReadCloser, error) { return os.Open(path) }
	}
	return &Adapter{cfg: cfg, health: vendor.Health{State: vendor.HealthUnknown}}, nil
}

// Stats returns a snapshot of the counters.
func (a *Adapter) Stats() Stats {
	return Stats{Emitted: a.emitted.Load(), Skipped: a.skipped.Load()}
}

// Name implements vendor.VendorAdapter.
func (a *Adapter) Name() string { return a.cfg.Name }

// Connect opens the file and checks the header, so a bad path or a file
// without the drone-id column fails fast with a permanent error.
func (a *Adapter) Connect(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rc, err := a.cfg.Open()
	if err != nil {
		a.setHealth(vendor.HealthDown, err)
		return fmt.Errorf("csvreplay: open: %w: %w", err, vendor.ErrPermanent)
	}
	defer rc.Close()
	if _, err := a.readHeader(csv.NewReader(rc)); err != nil {
		a.setHealth(vendor.HealthDown, err)
		return fmt.Errorf("csvreplay: %w: %w", err, vendor.ErrPermanent)
	}
	a.setHealth(vendor.HealthHealthy, nil)
	return nil
}

// Subscribe replays the file into out. It returns nil at end of file (unless
// Loop is set) and ctx.Err() when cancelled.
func (a *Adapter) Subscribe(ctx context.Context, out chan<- vendor.RawRecord) error {
	for {
		n, err := a.replayOnce(ctx, out)
		if err != nil {
			return err
		}
		if !a.cfg.Loop {
			return nil
		}
		if n == 0 {
			err := fmt.Errorf("csvreplay: file produced no usable rows, refusing to loop: %w", vendor.ErrPermanent)
			a.setHealth(vendor.HealthDown, err)
			return err
		}
	}
}

func (a *Adapter) replayOnce(ctx context.Context, out chan<- vendor.RawRecord) (int, error) {
	rc, err := a.cfg.Open()
	if err != nil {
		a.setHealth(vendor.HealthDown, err)
		return 0, fmt.Errorf("csvreplay: open: %w: %w", err, vendor.ErrPermanent)
	}
	defer rc.Close()

	r := csv.NewReader(rc)
	header, err := a.readHeader(r)
	if err != nil {
		a.setHealth(vendor.HealthDown, err)
		return 0, fmt.Errorf("csvreplay: %w: %w", err, vendor.ErrPermanent)
	}

	var prevTS time.Time
	count := 0
	for {
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			return count, nil
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) { // bad quoting etc.: skip the row, keep going
				a.skip("unparsable row", "line", pe.Line, "err", pe.Err)
				continue
			}
			a.setHealth(vendor.HealthDown, err)
			return count, fmt.Errorf("csvreplay: read: %w", err)
		}
		if len(row) != len(header) {
			line, _ := r.FieldPos(0)
			a.skip("row has wrong number of fields", "line", line, "got", len(row), "want", len(header))
			continue
		}

		droneID := ""
		payload := make(map[string]any, len(header))
		for i, name := range header {
			cell := strings.TrimSpace(row[i])
			if name == a.cfg.DroneIDColumn {
				droneID = cell // keep ids as text: "007" must not become 7
				if cell != "" {
					payload[name] = cell
				}
				continue
			}
			if v, ok := rawutil.InferValue(cell); ok {
				payload[name] = v
			}
		}
		if droneID == "" {
			line, _ := r.FieldPos(0)
			a.skip("row has no drone id", "line", line)
			continue
		}

		if a.cfg.Speed > 0 {
			if ts, ok := rawutil.ParseTimestamp(payload[a.cfg.TimestampColumn]); ok {
				if !prevTS.IsZero() && ts.After(prevTS) {
					wait := time.Duration(float64(ts.Sub(prevTS)) / a.cfg.Speed)
					if wait > a.cfg.MaxDelay {
						wait = a.cfg.MaxDelay
					}
					if err := sleepCtx(ctx, wait); err != nil {
						return count, err
					}
				}
				prevTS = ts
			}
		}

		rec := vendor.RawRecord{
			Vendor:        a.cfg.Vendor,
			SourceDroneID: droneID,
			ReceivedAt:    a.cfg.Now().UTC(),
			Simulated:     true, // a replay is never live data
			Payload:       payload,
		}
		select {
		case out <- rec:
		case <-ctx.Done():
			return count, ctx.Err()
		}
		a.emitted.Add(1)
		count++
		a.setHealth(vendor.HealthHealthy, nil)
	}
}

func (a *Adapter) readHeader(r *csv.Reader) ([]string, error) {
	r.FieldsPerRecord = -1 // ragged rows are handled per row, not fatal
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("cannot read header row: %w", err)
	}
	seen := map[string]bool{}
	hasID := false
	for i, h := range header {
		h = strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")) // Excel adds a BOM
		header[i] = h
		if h == "" {
			return nil, fmt.Errorf("header column %d has no name", i+1)
		}
		if seen[h] {
			return nil, fmt.Errorf("duplicate header column %q", h)
		}
		seen[h] = true
		if h == a.cfg.DroneIDColumn {
			hasID = true
		}
	}
	if !hasID {
		return nil, fmt.Errorf("header has no %q column", a.cfg.DroneIDColumn)
	}
	return header, nil
}

func (a *Adapter) skip(msg string, args ...any) {
	a.skipped.Add(1)
	slog.Warn("csvreplay: "+msg, args...)
}

func (a *Adapter) setHealth(state vendor.HealthState, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.health.State = state
	if err != nil {
		a.health.LastError = err.Error()
		return
	}
	a.health.LastError = ""
	a.health.LastContact = a.cfg.Now().UTC()
}

// Health implements vendor.VendorAdapter.
func (a *Adapter) Health() vendor.Health {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.health
}

// Close implements vendor.VendorAdapter. Files are closed per pass, so this is a no-op.
func (a *Adapter) Close() error { return nil }

var _ vendor.VendorAdapter = (*Adapter)(nil)

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
