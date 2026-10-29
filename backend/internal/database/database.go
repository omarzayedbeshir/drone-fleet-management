package database

import (
	"context"
	"errors"
	"net"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context) (*pgxpool.Pool, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return nil, errors.New("DATABASE_URL is required; run database migrations before starting the API")
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL")
	}
	config.MaxConns = 10
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("could not initialize database pool")
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, connectionError(err)
	}
	return pool, nil
}

// Return actionable categories without including a connection string or server
// details that might contain credentials.
func connectionError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "28P01", "28000":
			return errors.New("database authentication failed: check the username/password in DATABASE_URL against the existing database; changing .env does not change an existing database volume's password")
		case "3D000":
			return errors.New("database does not exist: check the database name in DATABASE_URL and initialize the intended database")
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("database connection timed out: check the host/port in DATABASE_URL and whether PostgreSQL is running")
	}
	var networkErr *net.OpError
	if errors.As(err, &networkErr) {
		return errors.New("database is unreachable: check the host/port in DATABASE_URL and start the database service")
	}
	return errors.New("could not connect to database: check DATABASE_URL, PostgreSQL availability, and TLS settings")
}
