package database

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestConnectionErrorIsActionableAndRedacted(t *testing.T) {
	for _, test := range []struct {
		name  string
		cause error
		want  string
	}{
		{"bad password", &pgconn.PgError{Code: "28P01", Message: "secret-password"}, "authentication failed"},
		{"authentication denied", &pgconn.PgError{Code: "28000", Message: "secret-password"}, "authentication failed"},
		{"missing database", &pgconn.PgError{Code: "3D000", Message: "secret-password"}, "does not exist"},
		{"timeout", context.DeadlineExceeded, "timed out"},
		{"network", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("secret-password")}, "unreachable"},
		{"other", errors.New("secret-password"), "TLS settings"},
	} {
		t.Run(test.name, func(t *testing.T) {
			message := connectionError(fmt.Errorf("connection failed: %w", test.cause)).Error()
			if !strings.Contains(message, test.want) || strings.Contains(message, "secret-password") {
				t.Fatalf("unexpected diagnostic: %s", message)
			}
		})
	}
}
