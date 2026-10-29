// ardupilot-adapter binds one ArduPilot MAVLink endpoint to a registered drone.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/ardupilot"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/database"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		slog.Error("ArduPilot adapter stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	endpoint := flag.String("endpoint", "tcp://127.0.0.1:5760", "MAVLink TCP server or UDP listener endpoint")
	drone := flag.String("drone-id", "", "Registered ArduPilot drone UUID")
	actor := flag.String("actor-id", "", "Active fleet manager UUID in that drone's organisation")
	actorEmail := flag.String("actor-email", "", "Fleet manager email (alternative to actor-id)")
	serial := flag.String("drone-serial", "", "ArduPilot serial number in the actor's organisation (alternative to drone-id)")
	system := flag.Uint("system-id", 1, "MAVLink flight-controller system ID (1–254)")
	component := flag.Uint("component-id", 1, "MAVLink flight-controller component ID (1–255)")
	interval := flag.Duration("interval", time.Second, "Normalized observation interval (100ms–1m)")
	limit := flag.Uint("max-samples", 0, "Exit after this many accepted observations (0 runs continuously)")
	flag.Parse()
	if (*drone == "") == (*serial == "") || (*actor == "") == (*actorEmail == "") || *system < 1 || *system > 254 || *component < 1 || *component > 255 {
		return errors.New("choose exactly one of drone-id/drone-serial and actor-id/actor-email, plus valid MAVLink IDs")
	}
	if *actorEmail != "" {
		email, err := auth.NormalizeEmail(*actorEmail)
		if err != nil {
			return err
		}
		*actorEmail = email
	}
	*serial = strings.TrimSpace(*serial)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	var org string
	var flight, cycles int64
	err = pool.QueryRow(ctx, `SELECT d.id::text,u.id::text,d.organisation_id::text,d.flight_time_seconds,d.battery_cycles
 FROM drones d JOIN users u ON u.organisation_id=d.organisation_id
 WHERE (d.id::text=$1 OR ($1='' AND d.serial_number=$2))
 AND (u.id::text=$3 OR ($3='' AND u.email=$4))
 AND d.vendor='ardupilot' AND u.role='fleet_manager' AND u.is_active`, *drone, *serial, *actor, *actorEmail).Scan(drone, actor, &org, &flight, &cycles)
	if err != nil {
		return errors.New("binding requires an existing ardupilot drone and active fleet manager in the same organisation; apply migrations first")
	}
	adapter, err := ardupilot.New(ardupilot.Config{Endpoint: *endpoint, SystemID: uint8(*system), ComponentID: uint8(*component), Interval: *interval, FlightBaseline: flight, BatteryCycleBaseline: cycles})
	if err != nil {
		return err
	}
	store := &telemetry.PostgresStore{Pool: pool}
	var accepted uint
	healthDone := make(chan struct{})
	defer func() { stop(); <-healthDone }()
	go func() {
		defer close(healthDone)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				data, _ := json.Marshal(adapter.Health())
				slog.Info("ArduPilot health", "health", string(data))
			}
		}
	}()
	slog.Info("ArduPilot adapter starting", "endpoint", *endpoint, "drone_id", *drone, "system_id", *system, "component_id", *component)
	err = adapter.Run(ctx, func(ctx context.Context, sample telemetry.Sample) error {
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := checkActor(attempt, pool, org, *actor); err != nil {
			return err
		}
		raw, err := json.Marshal(sample)
		if err != nil {
			return &adapters.PermanentError{Err: err}
		}
		result, err := store.Ingest(attempt, org, *actor, *drone, raw)
		if err != nil {
			return err
		}
		accepted++
		slog.Info("ArduPilot telemetry accepted", "event_id", sample.EventID, "duplicate", result.Duplicate, "latest_updated", result.LatestUpdated)
		if *limit > 0 && accepted >= *limit {
			stop()
		}
		return nil
	})
	data, _ := json.Marshal(adapter.Health())
	slog.Info("ArduPilot final health", "health", string(data))
	if err != nil {
		return err
	}
	if *limit > 0 && accepted < *limit {
		return fmt.Errorf("adapter stopped before accepting %d observations", *limit)
	}
	return nil
}
func checkActor(ctx context.Context, pool *pgxpool.Pool, org, actor string) error {
	var active bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND organisation_id=$2 AND role='fleet_manager' AND is_active)`, actor, org).Scan(&active); err != nil {
		return err
	}
	if !active {
		return &adapters.PermanentError{Err: errors.New("adapter actor is no longer an active fleet manager in the bound organisation")}
	}
	return nil
}
