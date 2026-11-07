// vendor-adapter binds one CSV or confirmed DJI source to a registered DJI drone.
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
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/csvreplay"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/dji"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/credentials"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/database"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/ingestion"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/vendor"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Vendor adapter stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {

	simulatedFlag := flag.Bool("simulated", false, "Label a mock DJI endpoint as simulated")
	mode := flag.String("source", "csv", "csv or dji")
	path := flag.String("csv", "testdata/dji_sample.csv", "Recorded DJI-format CSV path")
	speed := flag.Float64("speed", 1, "Replay speed (0 is immediate)")
	endpoint := flag.String("endpoint", "", "Confirmed DJI API base URL")
	drone := flag.String("drone-id", "", "Registered DJI drone UUID")
	actor := flag.String("actor-id", "", "Active fleet manager UUID")
	actorEmail := flag.String("actor-email", "", "Active fleet manager email")
	serial := flag.String("drone-serial", "", "Registered DJI drone serial")
	limit := flag.Uint("max-samples", 0, "Stop after accepted observations (0 runs to EOF)")
	flag.Parse()
	if (*drone == "") == (*serial == "") || (*actor == "") == (*actorEmail == "") {
		return errors.New("choose exactly one of drone-id/drone-serial and actor-id/actor-email")
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
 AND d.vendor='dji' AND u.role='fleet_manager' AND u.is_active`, *drone, *serial, *actor, *actorEmail).Scan(drone, actor, &org, &flight, &cycles)
	if err != nil {
		return errors.New("binding requires an existing DJI drone and active fleet manager in the same organisation; apply migrations first")
	}

	var boundSerial string
	if err := pool.QueryRow(ctx, `SELECT serial_number FROM drones WHERE organisation_id=$1 AND id=$2`, org, *drone).Scan(&boundSerial); err != nil {
		return err
	}
	var source vendor.VendorAdapter
	simulated := *mode == "csv" || *simulatedFlag
	switch *mode {
	case "csv":
		source, err = csvreplay.New(csvreplay.Config{Path: *path, Vendor: "dji", Speed: *speed})
	case "dji":
		source, err = dji.New(dji.Config{BaseURL: *endpoint, Credentials: credentials.EnvProvider{}, DeviceSerials: []string{boundSerial}})
	default:
		return errors.New("source must be csv or dji")
	}
	if err != nil {
		return err
	}
	adapter, err := ingestion.New(ingestion.Config{Source: source, Serial: boundSerial, Vendor: "dji", Simulated: simulated})
	if err != nil {
		return err
	}
	slog.Info("Telemetry source starting", "source", *mode, "simulated", simulated, "drone_id", *drone)

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
				slog.Info("Vendor health", "health", string(data))
			}
		}
	}()

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
		_, err = store.Ingest(attempt, org, *actor, *drone, raw)
		if err != nil {
			return err
		}
		accepted++
		if *limit > 0 && accepted >= *limit {
			stop()
		}
		return nil
	})
	data, _ := json.Marshal(adapter.Health())
	slog.Info("Vendor final health", "health", string(data))
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
