package mockfeed_test

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/csvreplay"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/mockfeed"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/vendor"
)

// The R-02 fallback only works if a recorded sample replayed through the CSV
// adapter yields the same raw records as the live adapter would. This pins it.
func TestCSVRoundTripPreservesPayloads(t *testing.T) {
	feed := mockfeed.New(mockfeed.Config{MaxRecords: 30, IncludeImagery: true})
	ch := make(chan vendor.RawRecord, 30)
	if err := feed.Subscribe(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	close(ch)
	var want []vendor.RawRecord
	for r := range ch {
		want = append(want, r)
	}

	var buf bytes.Buffer
	if err := mockfeed.WriteCSV(&buf, want); err != nil {
		t.Fatal(err)
	}
	a, err := csvreplay.New(csvreplay.Config{
		Vendor: "mock",
		Open:   func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(buf.Bytes())), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan vendor.RawRecord, 100)
	if err := a.Subscribe(ctx, out); err != nil {
		t.Fatal(err)
	}
	close(out)

	i := 0
	for got := range out {
		if got.SourceDroneID != want[i].SourceDroneID {
			t.Fatalf("row %d: drone %q != %q", i, got.SourceDroneID, want[i].SourceDroneID)
		}
		if !reflect.DeepEqual(got.Payload, want[i].Payload) {
			t.Fatalf("row %d payload differs:\n got %#v\nwant %#v", i, got.Payload, want[i].Payload)
		}
		i++
	}
	if i != len(want) {
		t.Fatalf("round trip returned %d rows, want %d", i, len(want))
	}
}
