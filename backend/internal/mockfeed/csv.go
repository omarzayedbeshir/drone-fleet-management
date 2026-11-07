package mockfeed

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/vendor"
)

// WriteCSV writes records as a CSV file the csvreplay adapter can read back
// into identical payloads. Columns: device_sn and timestamp first, then the
// remaining keys sorted. Used to generate the recorded DJI-format sample that
// stands in for live DJI if access is delayed (R-02).
func WriteCSV(w io.Writer, recs []vendor.RawRecord) error {
	set := map[string]bool{}
	for _, r := range recs {
		for k := range r.Payload {
			set[k] = true
		}
	}
	var rest []string
	for k := range set {
		if k != "device_sn" && k != "timestamp" {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	cols := append([]string{"device_sn", "timestamp"}, rest...)

	cw := csv.NewWriter(w)
	if err := cw.Write(cols); err != nil {
		return err
	}
	for _, r := range recs {
		row := make([]string, len(cols))
		for i, c := range cols {
			v, ok := r.Payload[c]
			if !ok {
				continue
			}
			switch t := v.(type) {
			case float64:
				row[i] = strconv.FormatFloat(t, 'f', -1, 64)
			default:
				row[i] = fmt.Sprint(t)
			}
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
