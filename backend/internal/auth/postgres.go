package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ Pool *pgxpool.Pool }

func (s *PostgresStore) FindCredentials(ctx context.Context, email string) (Credentials, error) {
	var c Credentials
	err := s.Pool.QueryRow(ctx, `SELECT id::text, organisation_id::text, name, email, role, password_hash, is_active
        FROM users WHERE email = $1`, email).Scan(&c.User.ID, &c.User.OrganisationID, &c.User.Name,
		&c.User.Email, &c.User.Role, &c.PasswordHash, &c.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrUnauthenticated
	}
	return c, err
}

// Lock the user before session issuance so a concurrent password/role change
// either sees and revokes this session or is observed before it is issued.
func (s *PostgresStore) CreateSession(ctx context.Context, credentials Credentials, token string) (User, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	var user User
	err = tx.QueryRow(ctx, `SELECT id::text, organisation_id::text, name, email, role FROM users
        WHERE id = $1 AND password_hash = $2 AND is_active FOR UPDATE`,
		credentials.User.ID, credentials.PasswordHash).Scan(&user.ID, &user.OrganisationID, &user.Name, &user.Email, &user.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUnauthenticated
	}
	if err != nil {
		return User{}, err
	}
	_, err = tx.Exec(ctx, `DELETE FROM auth_sessions WHERE user_id = $1
        AND (expires_at <= now() OR last_seen_at <= now() - interval '30 minutes')`, user.ID)
	if err != nil {
		return User{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO auth_sessions (token_hash, user_id) VALUES ($1, $2)`, TokenHash(token), user.ID)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *PostgresStore) ResolveSession(ctx context.Context, token string) (User, error) {
	var user User
	err := s.Pool.QueryRow(ctx, `UPDATE auth_sessions AS sessions SET last_seen_at = clock_timestamp()
        FROM users WHERE sessions.token_hash = $1 AND sessions.user_id = users.id
        AND users.is_active AND sessions.expires_at > clock_timestamp()
        AND sessions.last_seen_at > clock_timestamp() - interval '30 minutes'
        RETURNING users.id::text, users.organisation_id::text, users.name, users.email, users.role`,
		TokenHash(token)).Scan(&user.ID, &user.OrganisationID, &user.Name, &user.Email, &user.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUnauthenticated
	}
	return user, err
}

func (s *PostgresStore) DeleteSession(ctx context.Context, token string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM auth_sessions WHERE token_hash = $1`, TokenHash(token))
	return err
}

func (s *PostgresStore) ChangePassword(ctx context.Context, id, oldHash, newHash string) error {
	result, err := s.Pool.Exec(ctx, `UPDATE users SET password_hash = $1
        WHERE id = $2 AND password_hash = $3 AND is_active`, newHash, id, oldHash)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrUnauthenticated
	}
	return nil
}
