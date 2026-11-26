package flightsummary

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Counts struct {
	Observations int64 `json:"observations"`
	InMission    int64 `json:"in_mission_observations"`
}

type Source struct {
	Counts
	DroneID   string    `json:"drone_id"`
	DroneName string    `json:"drone_name"`
	First     time.Time `json:"first_observation"`
	Last      time.Time `json:"last_observation"`
}

type Facts struct {
	Counts
	Aircraft          int `json:"aircraft_with_telemetry"`
	AircraftInMission int `json:"aircraft_observed_in_mission"`
}

type PostgresReader struct {
	Pool interface {
		Query(context.Context, string, ...any) (pgx.Rows, error)
	}
}

func (s PostgresReader) Sources(ctx context.Context, org string, day time.Time) ([]Source, error) {
	rows, err := s.Pool.Query(ctx, `SELECT d.id::text, d.name, min(t.observed_at), max(t.observed_at),
		count(*), count(*) FILTER (WHERE t.status = 'in_mission')
		FROM telemetry_samples t JOIN drones d ON (d.organisation_id, d.id) = (t.organisation_id, t.drone_id)
		WHERE t.organisation_id = $1 AND t.observed_at >= $2 AND t.observed_at < $3
		GROUP BY d.id, d.name ORDER BY d.name, d.id`, org, day, day.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := []Source{}
	for rows.Next() {
		var source Source
		if err := rows.Scan(&source.DroneID, &source.DroneName, &source.First, &source.Last,
			&source.Observations, &source.InMission); err != nil {
			return nil, err
		}
		source.First, source.Last = source.First.UTC(), source.Last.UTC()
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

type Reader interface {
	Sources(context.Context, string, time.Time) ([]Source, error)
}

type Writer interface {
	Narrate(context.Context, Facts) (string, error)
}

type Service struct {
	Reader Reader
	Writer Writer
}

type Report struct {
	Day       string   `json:"day"`
	Facts     Facts    `json:"facts"`
	Sources   []Source `json:"sources"`
	Narrative string   `json:"narrative"`
	Status    string   `json:"status"`
}

func (s Service) Generate(ctx context.Context, org string, day time.Time, useAI bool) (Report, error) {
	sources, err := s.Reader.Sources(ctx, org, day)
	if err != nil {
		return Report{}, err
	}
	facts := Facts{Aircraft: len(sources)}
	for _, source := range sources {
		facts.Observations += source.Observations
		facts.InMission += source.InMission
		if source.InMission > 0 {
			facts.AircraftInMission++
		}
	}
	report := Report{Day: day.Format(time.DateOnly), Facts: facts, Sources: sources, Status: "facts_only"}
	if facts.Aircraft == 0 {
		report.Status = "no_data"
		report.Narrative = "No telemetry observations were recorded for this UTC day."
		return report, nil
	}
	report.Narrative = fmt.Sprintf("Telemetry contained %d observations across %d aircraft. It reported an in-mission status in %d observations across %d aircraft.",
		facts.Observations, facts.Aircraft, facts.InMission, facts.AircraftInMission)
	if !useAI {
		return report, nil
	}
	if s.Writer == nil {
		report.Status = "ai_not_configured"
		return report, nil
	}
	report.Status = "ai_unavailable"
	narrative, err := s.Writer.Narrate(ctx, facts)
	if err == nil && strings.TrimSpace(narrative) != "" {
		report.Status = "ai_generated"
		report.Narrative = strings.TrimSpace(narrative)
	}
	return report, nil
}
