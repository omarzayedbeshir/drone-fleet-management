// Command mockfeed runs the mock vendor as an HTTP server, for local
// development, docker-compose and live demos of outage handling (NFR-03).
//
//	go run ./cmd/mockfeed -addr :8089 -token dev-token -drones 5 -imagery
//
// Flip failures while it runs:
//
//	curl -X PUT localhost:8089/admin/faults -d '{"down":true}'
//	curl -X PUT localhost:8089/admin/faults -d '{"latency_ms":3000}'
//	curl -X PUT localhost:8089/admin/faults -d '{"malformed_rate":0.3}'
//	curl -X PUT localhost:8089/admin/faults -d '{}'            # all clear
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/mockfeed"
)

func main() {
	addr := flag.String("addr", ":8089", "listen address")
	token := flag.String("token", os.Getenv("MOCKFEED_TOKEN"), "required API token (empty = no auth)")
	drones := flag.Int("drones", 3, "number of simulated drones")
	seed := flag.Int64("seed", 1, "random seed")
	imagery := flag.Bool("imagery", false, "include camera/payload fields (for privacy filter tests)")
	latency := flag.Duration("latency", 0, "initial injected latency")
	malformed := flag.Float64("malformed", 0, "initial malformed-record rate 0..1")
	down := flag.Bool("down", false, "start in the 'vendor down' state")
	flag.Parse()

	ids := make([]string, *drones)
	for i := range ids {
		ids[i] = fmt.Sprintf("MOCK-%04d", i+1)
	}
	feed := mockfeed.New(mockfeed.Config{
		DroneIDs: ids, Seed: *seed, IncludeImagery: *imagery,
		Faults: mockfeed.Faults{Down: *down, Latency: *latency, MalformedRate: *malformed},
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mockfeed.NewServer(feed, mockfeed.ServerConfig{Token: *token}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	slog.Info("mock vendor feed listening", "addr", *addr, "drones", len(ids), "auth", *token != "")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
}
