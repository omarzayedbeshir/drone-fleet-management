package mockfeed

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"
)

// DefaultAuthHeader is the header the mock expects the API token in.
const DefaultAuthHeader = "X-User-Token"

type envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ServerConfig configures the HTTP face of the mock.
type ServerConfig struct {
	Token      string // if set, requests must carry it in AuthHeader (else 401)
	AuthHeader string // default DefaultAuthHeader
}

// NewServer returns an http.Handler that imitates a vendor cloud API:
//
//	GET  /devices                    -> {"code":0,"data":[{"device_sn":"..."}]}
//	GET  /devices/{sn}/telemetry     -> {"code":0,"data":{ ...nested native record... }}
//	GET  /healthz                    -> 200 (never faulted, for container health checks)
//	GET  /admin/faults               -> current faults
//	PUT  /admin/faults               -> replace faults (JSON, see faultsJSON)
//
// The three vendor routes honour the injected faults: Down returns 503,
// Latency delays the response, MalformedRate corrupts telemetry bodies. The
// admin routes let a demo flip failures on and off while the system runs.
func NewServer(feed *Feed, cfg ServerConfig) http.Handler {
	if cfg.AuthHeader == "" {
		cfg.AuthHeader = DefaultAuthHeader
	}
	s := &server{feed: feed, cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /admin/faults", s.getFaults)
	mux.HandleFunc("PUT /admin/faults", s.putFaults)
	mux.HandleFunc("GET /devices", s.vendor(s.listDevices))
	mux.HandleFunc("GET /devices/{sn}/telemetry", s.vendor(s.telemetry))
	return mux
}

type server struct {
	feed *Feed
	cfg  ServerConfig
}

// vendor wraps a handler with auth, latency and down injection.
func (s *server) vendor(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		faults := s.feed.Faults()
		if err := sleep(r.Context(), faults.Latency); err != nil {
			return // client gave up (timeout): nothing to write
		}
		if faults.Down {
			writeJSON(w, http.StatusServiceUnavailable, envelope{Code: 503, Message: "vendor unavailable (injected fault)"})
			return
		}
		if s.cfg.Token != "" {
			got := r.Header.Get(s.cfg.AuthHeader)
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) != 1 {
				writeJSON(w, http.StatusUnauthorized, envelope{Code: 401, Message: "invalid token"})
				return
			}
		}
		h(w, r)
	}
}

func (s *server) listDevices(w http.ResponseWriter, _ *http.Request) {
	type dev struct {
		SN string `json:"device_sn"`
	}
	var list []dev
	for _, id := range s.feed.Devices() {
		list = append(list, dev{SN: id})
	}
	writeJSON(w, http.StatusOK, envelope{Code: 0, Message: "ok", Data: list})
}

func (s *server) telemetry(w http.ResponseWriter, r *http.Request) {
	sn := r.PathValue("sn")
	if !s.feed.gen.has(sn) {
		writeJSON(w, http.StatusNotFound, envelope{Code: 404, Message: "unknown device"})
		return
	}
	faults := s.feed.Faults()
	native, kind := s.feed.gen.next(sn, faults.MalformedRate, intersect(AllKinds, faults.MalformedKinds))
	body, _ := json.Marshal(envelope{Code: 0, Message: "ok", Data: native})
	if kind == KindBrokenJSON {
		body = body[:len(body)/2] // truncated body: not valid JSON
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// faultsJSON is the wire form of Faults for the admin endpoint.
type faultsJSON struct {
	Down           bool            `json:"down"`
	LatencyMS      int64           `json:"latency_ms"`
	MalformedRate  float64         `json:"malformed_rate"`
	MalformedKinds []MalformedKind `json:"malformed_kinds"`
}

func (s *server) getFaults(w http.ResponseWriter, _ *http.Request) {
	f := s.feed.Faults()
	writeJSON(w, http.StatusOK, faultsJSON{
		Down: f.Down, LatencyMS: f.Latency.Milliseconds(),
		MalformedRate: f.MalformedRate, MalformedKinds: f.MalformedKinds,
	})
}

func (s *server) putFaults(w http.ResponseWriter, r *http.Request) {
	var in faultsJSON
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{Code: 400, Message: err.Error()})
		return
	}
	if in.MalformedRate < 0 || in.MalformedRate > 1 || in.LatencyMS < 0 {
		writeJSON(w, http.StatusBadRequest, envelope{Code: 400, Message: "malformed_rate must be 0..1 and latency_ms >= 0"})
		return
	}
	valid := map[MalformedKind]bool{}
	for _, k := range AllKinds {
		valid[k] = true
	}
	for _, k := range in.MalformedKinds {
		if !valid[k] {
			writeJSON(w, http.StatusBadRequest, envelope{Code: 400, Message: "unknown malformed kind: " + string(k)})
			return
		}
	}
	s.feed.SetFaults(Faults{
		Down:           in.Down,
		Latency:        time.Duration(in.LatencyMS) * time.Millisecond,
		MalformedRate:  in.MalformedRate,
		MalformedKinds: in.MalformedKinds,
	})
	s.getFaults(w, r)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
