package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/floatinginbits/nabu/internal/auth/sqlcgen"
)

type PostgresRefreshRepository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewPostgresRefreshRepository(pool *pgxpool.Pool) *PostgresRefreshRepository {
	return &PostgresRefreshRepository{pool: pool, q: sqlcgen.New(pool)}
}

func (r *PostgresRefreshRepository) Create(ctx context.Context, familyID, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) (RefreshToken, error) {
	row, err := r.q.CreateRefreshToken(ctx, sqlcgen.CreateRefreshTokenParams{
		FamilyID:  familyID,
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return RefreshToken{}, fmt.Errorf("creating refresh token: %w", err)
	}
	return fromRow(row), nil
}

// Rotate runs the reuse-detection state machine inside one transaction, with
// the presented row held FOR UPDATE so concurrent refreshes of the same token
// serialize rather than both minting or both tripping reuse detection.
func (r *PostgresRefreshRepository) Rotate(ctx context.Context, presentedHash, newHash []byte, newExpiry time.Time, graceWindow time.Duration, now time.Time) (RefreshToken, RotateOutcome, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return RefreshToken{}, RotateInvalid, fmt.Errorf("beginning rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)

	row, err := q.GetRefreshTokenByHashForUpdate(ctx, presentedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefreshToken{}, RotateInvalid, nil
	}
	if err != nil {
		return RefreshToken{}, RotateInvalid, fmt.Errorf("locking refresh token: %w", err)
	}
	presented := fromRow(row)

	// Revoked (family already killed) or past expiry: nothing to rotate.
	if row.RevokedAt.Valid || !row.ExpiresAt.After(now) {
		return RefreshToken{}, RotateInvalid, nil
	}

	// Already rotated once: either a concurrent refresh (issue a sibling) or a
	// genuine reuse of a stale token (revoke the family).
	if row.ReplacedAt.Valid {
		// Symmetric on purpose. replaced_at is stamped from the app instance that
		// rotated, and now comes from whichever instance handles this request, so
		// the difference carries their clock skew and can be negative for a
		// genuinely concurrent refresh. Comparing the magnitude treats "replaced
		// a moment ago" the same in either direction, which is what the grace
		// window means; a real stale replay sits far outside the window whichever
		// way the clocks lean.
		if elapsed := now.Sub(row.ReplacedAt.Time); elapsed.Abs() <= graceWindow {
			sib, err := q.CreateRefreshToken(ctx, sqlcgen.CreateRefreshTokenParams{
				FamilyID:  row.FamilyID,
				UserID:    row.UserID,
				TokenHash: newHash,
				ExpiresAt: newExpiry,
			})
			if err != nil {
				return presented, RotateInvalid, fmt.Errorf("minting sibling token: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return presented, RotateInvalid, fmt.Errorf("committing rotation: %w", err)
			}
			return fromRow(sib), RotateOK, nil
		}
		if err := q.RevokeRefreshTokenFamily(ctx, row.FamilyID); err != nil {
			return presented, RotateInvalid, fmt.Errorf("revoking family on reuse: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return presented, RotateInvalid, fmt.Errorf("committing revocation: %w", err)
		}
		return presented, RotateReuse, nil
	}

	// Happy path: mint the successor and link the presented row to it.
	succ, err := q.CreateRefreshToken(ctx, sqlcgen.CreateRefreshTokenParams{
		FamilyID:  row.FamilyID,
		UserID:    row.UserID,
		TokenHash: newHash,
		ExpiresAt: newExpiry,
	})
	if err != nil {
		return presented, RotateInvalid, fmt.Errorf("minting successor token: %w", err)
	}
	if err := q.MarkRefreshTokenReplaced(ctx, sqlcgen.MarkRefreshTokenReplacedParams{
		SuccessorID: uuid.NullUUID{UUID: succ.ID, Valid: true},
		ReplacedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		ID:          row.ID,
	}); err != nil {
		return presented, RotateInvalid, fmt.Errorf("marking token replaced: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return presented, RotateInvalid, fmt.Errorf("committing rotation: %w", err)
	}
	return fromRow(succ), RotateOK, nil
}

func (r *PostgresRefreshRepository) RevokeFamilyByHash(ctx context.Context, tokenHash []byte) error {
	if err := r.q.RevokeRefreshTokenFamilyByHash(ctx, tokenHash); err != nil {
		return fmt.Errorf("revoking family by hash: %w", err)
	}
	return nil
}

func fromRow(row sqlcgen.RefreshToken) RefreshToken {
	t := RefreshToken{
		ID:        row.ID,
		FamilyID:  row.FamilyID,
		UserID:    row.UserID,
		ExpiresAt: row.ExpiresAt,
	}
	if row.RevokedAt.Valid {
		revoked := row.RevokedAt.Time
		t.RevokedAt = &revoked
	}
	if row.ReplacedAt.Valid {
		replaced := row.ReplacedAt.Time
		t.ReplacedAt = &replaced
	}
	return t
}
