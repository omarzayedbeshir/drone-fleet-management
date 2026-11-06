// Package djisource implements the R-02 / NFR-08 decision in one place:
// use live DJI when it is configured, otherwise replay the recorded
// DJI-format sample through the CSV adapter, labelled as simulated.
//
// Either way the pipeline sees RawRecord.Vendor == "dji", so the DJI field
// mapping in the normalizer works for both. Only RawRecord.Simulated differs,
// and the demo/dashboard must surface it.
package djisource

import (
	"context"
	"errors"
	"log/slog"

	"github.com/omarzayedbeshir/drone-fleet-management/internal/adapters/csvreplay"
	"github.com/omarzayedbeshir/drone-fleet-management/internal/adapters/dji"
	"github.com/omarzayedbeshir/drone-fleet-management/internal/credentials"
	"github.com/omarzayedbeshir/drone-fleet-management/internal/vendor"
)

// Selection says which source was chosen and why.
type Selection struct {
	Adapter   vendor.VendorAdapter
	Simulated bool
	Reason    string
}

// Select returns the live DJI adapter when live.BaseURL is set and credentials
// exist for live.CredentialRef; otherwise the CSV replay of the recorded sample.
// A credentials error other than "not found" is returned, not hidden: a broken
// secret store must not silently turn into simulated data.
func Select(ctx context.Context, live dji.Config, sample csvreplay.Config) (Selection, error) {
	reason := ""
	switch {
	case live.BaseURL == "":
		reason = "DJI base URL not configured"
	case live.Credentials == nil:
		reason = "no credentials provider configured"
	default:
		ref := live.CredentialRef
		if ref == "" {
			ref = "dji"
		}
		_, err := live.Credentials.Get(ctx, ref)
		switch {
		case err == nil:
			a, err := dji.New(live)
			if err != nil {
				return Selection{}, err
			}
			return Selection{Adapter: a, Reason: "DJI credentials found"}, nil
		case errors.Is(err, credentials.ErrNotFound):
			reason = "no DJI credentials found"
		default:
			return Selection{}, err
		}
	}

	sample.Vendor = dji.VendorName // downstream applies the DJI mapping
	if sample.Name == "" {
		sample.Name = "csv"
	}
	a, err := csvreplay.New(sample)
	if err != nil {
		return Selection{}, err
	}
	slog.Warn("using SIMULATED DJI data (recorded sample via CSV adapter)", "reason", reason)
	return Selection{Adapter: a, Simulated: true, Reason: reason}, nil
}
