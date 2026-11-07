package mockfeed

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

// MalformedKind names one way a record can be corrupted on purpose. Every kind
// is something the validation stage (Ziad, UC-12a/UC-12c) must catch, or the
// privacy filter (Ali, SOC-03) must handle.
type MalformedKind string

const (
	KindMissingField MalformedKind = "missing_field" // a required field is absent
	KindWrongType    MalformedKind = "wrong_type"    // a number arrives as text
	KindOutOfRange   MalformedKind = "out_of_range"  // physically impossible value
	KindBadTimestamp MalformedKind = "bad_timestamp" // timestamp cannot be parsed
	KindDuplicate    MalformedKind = "duplicate"     // exact repeat of the previous record
	KindEmpty        MalformedKind = "empty"         // record with no fields at all
	KindBrokenJSON   MalformedKind = "broken_json"   // truncated body (HTTP server only)
)

// AllKinds lists every malformed kind in a stable order.
var AllKinds = []MalformedKind{
	KindMissingField, KindWrongType, KindOutOfRange,
	KindBadTimestamp, KindDuplicate, KindEmpty, KindBrokenJSON,
}

// adapterKinds are the kinds that make sense when records are handed over as
// Go maps (no wire format to break).
var adapterKinds = []MalformedKind{
	KindMissingField, KindWrongType, KindOutOfRange,
	KindBadTimestamp, KindDuplicate, KindEmpty,
}

type droneState struct {
	lat, lon    float64
	battery     float64
	cycles      float64
	flightTime  float64
	heading     float64
	step        int
	lastEmitted map[string]any
	phaseOffset float64
}

// generator produces DJI-style nested telemetry for a fixed set of drones.
// It is deterministic for a given seed (apart from the timestamp, which comes
// from the injected clock).
type generator struct {
	mu      sync.Mutex
	rng     *rand.Rand
	now     func() time.Time
	drones  map[string]*droneState
	imagery bool
}

func newGenerator(ids []string, seed int64, now func() time.Time, imagery bool) *generator {
	g := &generator{
		rng:     rand.New(rand.NewSource(seed)),
		now:     now,
		drones:  make(map[string]*droneState, len(ids)),
		imagery: imagery,
	}
	for i, id := range ids {
		g.drones[id] = &droneState{
			lat:         30.0444 + float64(i)*0.01,
			lon:         31.2357 + float64(i)*0.01,
			battery:     100 - float64(g.rng.Intn(30)),
			cycles:      float64(20 + g.rng.Intn(200)),
			flightTime:  float64(3600 * (1 + g.rng.Intn(100))),
			heading:     float64(g.rng.Intn(360)),
			phaseOffset: float64(i),
		}
	}
	return g
}

func (g *generator) has(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.drones[id]
	return ok
}

// next advances one drone by one step and returns its native (nested) record.
// If malformedRate triggers, the record is corrupted and the kind is returned.
func (g *generator) next(id string, rate float64, allowed []MalformedKind) (map[string]any, MalformedKind) {
	g.mu.Lock()
	defer g.mu.Unlock()

	d, ok := g.drones[id]
	if !ok {
		return nil, ""
	}
	d.step++
	d.lat += 0.0001 * (1 + 0.1*d.phaseOffset)
	d.lon += 0.00008
	d.heading = float64(int(d.heading+3) % 360)
	d.battery -= 0.2
	if d.battery < 15 {
		d.battery = 100
		d.cycles++
	}
	d.flightTime++

	faults := []any{}
	if g.rng.Intn(50) == 0 {
		faults = append(faults, "0x0A01") // pretend: motor temperature warning
	}

	rec := map[string]any{
		"device_sn":        id,
		"timestamp":        float64(g.now().UTC().UnixMilli()), // epoch milliseconds
		"latitude":         round(d.lat, 6),
		"longitude":        round(d.lon, 6),
		"height":           40 + 10*float64(d.step%5),
		"horizontal_speed": 5.5,
		"vertical_speed":   0.0,
		"attitude_head":    d.heading,
		"battery": map[string]any{
			"capacity_percent": round(d.battery, 1),
			"voltage":          22800.0, // millivolts: normalizer converts to volts
			"temperature":      31.5,    // degrees Celsius
			"loop_times":       d.cycles,
		},
		"total_flight_time": d.flightTime, // seconds
		"mode_code":         5.0,
		"fault_codes":       faults,
	}
	if g.imagery {
		// Fields the privacy filter MUST drop (SOC-03 / UC-12b).
		rec["camera_frame_b64"] = "/9j/4AAQSkZJRgABAQ=="
		rec["payload"] = map[string]any{"camera": map[string]any{"stream_url": "rtmp://example.invalid/live"}}
	}

	if rate <= 0 || g.rng.Float64() >= rate {
		d.lastEmitted = deepCopy(rec)
		return rec, ""
	}

	kind := g.pickKind(allowed)
	switch kind {
	case KindMissingField:
		missing := []string{"latitude", "longitude", "timestamp", "battery"}
		delete(rec, missing[g.rng.Intn(len(missing))])
	case KindWrongType:
		rec["latitude"] = "north-ish"
	case KindOutOfRange:
		rec["latitude"] = 123.4
		rec["battery"].(map[string]any)["capacity_percent"] = 250.0
	case KindBadTimestamp:
		rec["timestamp"] = "not-a-time"
	case KindDuplicate:
		if d.lastEmitted != nil {
			return deepCopy(d.lastEmitted), KindDuplicate
		}
		kind = "" // nothing to duplicate yet: send a normal record
	case KindEmpty:
		rec = map[string]any{}
	case KindBrokenJSON:
		// record stays intact; the HTTP server truncates the encoded body.
	}
	if kind != KindDuplicate && kind != KindEmpty {
		d.lastEmitted = deepCopy(rec)
	}
	return rec, kind
}

func (g *generator) pickKind(allowed []MalformedKind) MalformedKind {
	if len(allowed) == 0 {
		return ""
	}
	return allowed[g.rng.Intn(len(allowed))]
}

func deepCopy(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case map[string]any:
			out[k] = deepCopy(t)
		case []any:
			out[k] = append([]any(nil), t...)
		default:
			out[k] = v
		}
	}
	return out
}

// intersect returns the kinds in base that the caller allows. An empty
// allowed list means "everything in base".
func intersect(base, allowed []MalformedKind) []MalformedKind {
	if len(allowed) == 0 {
		return base
	}
	set := map[MalformedKind]bool{}
	for _, k := range allowed {
		set[k] = true
	}
	var out []MalformedKind
	for _, k := range base {
		if set[k] {
			out = append(out, k)
		}
	}
	return out
}

func round(v float64, places int) float64 {
	p := math.Pow10(places)
	return math.Round(v*p) / p
}
