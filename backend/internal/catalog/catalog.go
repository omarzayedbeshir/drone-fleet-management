// Package catalog defines stable vendor codes independently of adapter modules.
package catalog

import (
	"context"
	"regexp"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func ValidSource(code string) bool { return codePattern.MatchString(code) }
func ValidVendor(code string) bool { return code != "csv" && ValidSource(code) }

type Vendor struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

var Builtins = []Vendor{{Code: "dji", Label: "DJI"}, {Code: "ardupilot", Label: "ArduPilot"}}

// Install is an operator startup action, not a tenant-facing onboarding API.
// Entries are retained so removing a module cannot invalidate fleet history.
func Install(ctx context.Context, pool *pgxpool.Pool, vendors []Vendor) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	ordered := append([]Vendor(nil), vendors...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Code < ordered[j].Code })
	for _, v := range ordered {
		if _, err = tx.Exec(ctx, `INSERT INTO supported_vendors(code,label) VALUES($1,$2) ON CONFLICT(code) DO UPDATE SET label=EXCLUDED.label`, v.Code, v.Label); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
