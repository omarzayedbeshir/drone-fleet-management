// create-user provisions accounts with operator-level database access. There is
// deliberately no public registration endpoint or default password.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/database"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	name := flag.String("name", "", "User display name")
	email := flag.String("email", "", "Login email")
	role := flag.String("role", "", "system_administrator, fleet_manager, pilot, or maintenance_technician")
	orgID := flag.String("organisation-id", "", "Existing organisation UUID (tenant roles only)")
	orgName := flag.String("new-organisation", "", "Create an organisation with this name (fleet manager only)")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	*name = strings.TrimSpace(*name)
	*orgName = strings.TrimSpace(*orgName)
	if utf8.RuneCountInString(*name) < 1 || utf8.RuneCountInString(*name) > 200 {
		return errors.New("name must be 1-200 characters")
	}
	canonicalEmail, err := auth.NormalizeEmail(*email)
	if err != nil {
		return err
	}
	userRole := auth.Role(*role)
	if !userRole.Valid() {
		return errors.New("choose a valid role")
	}
	if userRole == auth.SystemAdministrator && (*orgID != "" || *orgName != "") {
		return errors.New("system administrators cannot belong to an organisation")
	}
	if *orgName != "" && (userRole != auth.FleetManager || *orgID != "" || utf8.RuneCountInString(*orgName) > 200) {
		return errors.New("new-organisation requires a fleet manager, a name of at most 200 characters, and no organisation-id")
	}
	if userRole != auth.SystemAdministrator && *orgID == "" && *orgName == "" {
		return errors.New("tenant users require an organisation-id or a new-organisation")
	}
	password, err := readPassword()
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := database.Open(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return errors.New("could not start account creation")
	}
	defer tx.Rollback(ctx)
	var tenant any
	if *orgName != "" {
		err := tx.QueryRow(ctx, `INSERT INTO organisations (name) VALUES ($1) RETURNING id::text`, *orgName).Scan(orgID)
		if err != nil {
			return errors.New("could not create organisation")
		}
	}
	if *orgID != "" {
		tenant = *orgID
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO users (organisation_id, name, email, password_hash, role)
        VALUES ($1, $2, $3, $4, $5) RETURNING id::text`, tenant, *name, canonicalEmail, hash, userRole).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return errors.New("an account with this email already exists")
			case "23503", "22P02":
				return errors.New("organisation-id must identify an existing organisation")
			}
		}
		return errors.New("could not create account")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("could not commit account creation")
	}
	fmt.Printf("Created %s account %s", userRole, id)
	if tenant != nil {
		fmt.Printf(" in organisation %s", *orgID)
	}
	fmt.Println()
	return nil
}

func readPassword() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Password (12-72 bytes): ")
		first, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", errors.New("could not read password")
		}
		fmt.Fprint(os.Stderr, "Confirm password: ")
		second, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", errors.New("could not read confirmation")
		}
		if string(first) != string(second) {
			return "", errors.New("passwords do not match")
		}
		return string(first), nil
	}
	// Piped input supports secret managers without putting passwords in argv.
	line, err := bufio.NewReader(io.LimitReader(os.Stdin, 74)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("could not read password")
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}
