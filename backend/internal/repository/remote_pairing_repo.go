package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// remotePairingRepository stores phone ↔ computer pairings (raw SQL).
//
// Every transition is a conditional UPDATE on the current status, so two
// requests racing for the same code or pairing cannot both succeed.
type remotePairingRepository struct {
	db *sql.DB
}

func NewRemotePairingRepository(db *sql.DB) service.RemotePairingRepository {
	return &remotePairingRepository{db: db}
}

const remotePairingColumns = `id, user_id, device_id, device_name, status, COALESCE(code_hash, ''), code_expires_at,
claim_attempts, phone_label, phone_public_key, COALESCE(token_hash, ''), created_at, claimed_at,
confirmed_at, revoked_at, last_used_at`

func scanRemotePairing(row rowScanner) (*service.RemotePairing, error) {
	var p service.RemotePairing
	var status string
	var codeExpires, claimed, confirmed, revoked, lastUsed sql.NullTime
	err := row.Scan(&p.ID, &p.UserID, &p.DeviceID, &p.DeviceName, &status, &p.CodeHash, &codeExpires,
		&p.ClaimAttempts, &p.PhoneLabel, &p.PhonePublicKey, &p.TokenHash, &p.CreatedAt, &claimed,
		&confirmed, &revoked, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrRemotePairingNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Status = service.RemotePairingStatus(status)
	p.CodeExpiresAt = remoteNullTime(codeExpires)
	p.ClaimedAt = remoteNullTime(claimed)
	p.ConfirmedAt = remoteNullTime(confirmed)
	p.RevokedAt = remoteNullTime(revoked)
	p.LastUsedAt = remoteNullTime(lastUsed)
	return &p, nil
}

func remoteNullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func (r *remotePairingRepository) Create(ctx context.Context, p *service.RemotePairing) error {
	return r.db.QueryRowContext(ctx, `
INSERT INTO remote_pairings (user_id, device_id, device_name, status, code_hash, code_expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, created_at`,
		p.UserID, p.DeviceID, p.DeviceName, string(p.Status), p.CodeHash, p.CodeExpiresAt,
	).Scan(&p.ID, &p.CreatedAt)
}

func (r *remotePairingRepository) GetForUser(ctx context.Context, userID, id int64) (*service.RemotePairing, error) {
	return scanRemotePairing(r.db.QueryRowContext(ctx,
		`SELECT `+remotePairingColumns+` FROM remote_pairings WHERE id = $1 AND user_id = $2`, id, userID))
}

func (r *remotePairingRepository) FindPendingByCode(ctx context.Context, userID int64, codeHash string, now time.Time) (*service.RemotePairing, error) {
	return scanRemotePairing(r.db.QueryRowContext(ctx, `
SELECT `+remotePairingColumns+` FROM remote_pairings
WHERE user_id = $1 AND status = 'pending' AND code_hash = $2 AND code_expires_at > $3
ORDER BY id DESC LIMIT 1`, userID, codeHash, now))
}

func (r *remotePairingRepository) RecordFailedClaim(ctx context.Context, userID int64, maxAttempts int, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `
UPDATE remote_pairings
SET claim_attempts = claim_attempts + 1,
    status = CASE WHEN claim_attempts + 1 >= $2 THEN 'revoked' ELSE status END,
    revoked_at = CASE WHEN claim_attempts + 1 >= $2 THEN $3 ELSE revoked_at END,
    code_hash = CASE WHEN claim_attempts + 1 >= $2 THEN NULL ELSE code_hash END
WHERE user_id = $1 AND status = 'pending'`, userID, maxAttempts, now)
	return err
}

func (r *remotePairingRepository) RevokeOpenForDevice(ctx context.Context, userID int64, deviceID string, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `
UPDATE remote_pairings
SET status = 'revoked', revoked_at = $3, code_hash = NULL, token_hash = NULL
WHERE user_id = $1 AND device_id = $2 AND status IN ('pending', 'claimed')`, userID, deviceID, now)
	return err
}

func (r *remotePairingRepository) Claim(ctx context.Context, id int64, phoneLabel, publicKey, tokenHash string, now time.Time) (bool, error) {
	// The code is single use: it is cleared as it is taken.
	result, err := r.db.ExecContext(ctx, `
UPDATE remote_pairings
SET status = 'claimed', phone_label = $2, phone_public_key = $3, token_hash = $4, claimed_at = $5, code_hash = NULL
WHERE id = $1 AND status = 'pending'`, id, phoneLabel, publicKey, tokenHash, now)
	return remoteAffectedOne(result, err)
}

func (r *remotePairingRepository) Confirm(ctx context.Context, userID int64, deviceID string, id int64, now time.Time) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
UPDATE remote_pairings
SET status = 'active', confirmed_at = $4
WHERE id = $1 AND user_id = $2 AND device_id = $3 AND status = 'claimed'`, id, userID, deviceID, now)
	return remoteAffectedOne(result, err)
}

func (r *remotePairingRepository) Revoke(ctx context.Context, userID, id int64, now time.Time) (*service.RemotePairing, error) {
	return scanRemotePairing(r.db.QueryRowContext(ctx, `
UPDATE remote_pairings
SET status = 'revoked', revoked_at = COALESCE(revoked_at, $3), code_hash = NULL, token_hash = NULL
WHERE id = $1 AND user_id = $2
RETURNING `+remotePairingColumns, id, userID, now))
}

func (r *remotePairingRepository) FindActiveByTokenHash(ctx context.Context, tokenHash string) (*service.RemotePairing, error) {
	return scanRemotePairing(r.db.QueryRowContext(ctx,
		`SELECT `+remotePairingColumns+` FROM remote_pairings WHERE token_hash = $1`, tokenHash))
}

func (r *remotePairingRepository) ListClaimedForDevice(ctx context.Context, userID int64, deviceID string) ([]service.RemotePairing, error) {
	return r.list(ctx, `SELECT `+remotePairingColumns+` FROM remote_pairings
WHERE user_id = $1 AND device_id = $2 AND status = 'claimed' ORDER BY id`, userID, deviceID)
}

func (r *remotePairingRepository) ListActiveForUser(ctx context.Context, userID int64) ([]service.RemotePairing, error) {
	return r.list(ctx, `SELECT `+remotePairingColumns+` FROM remote_pairings
WHERE user_id = $1 AND status = 'active' ORDER BY device_id, id`, userID)
}

func (r *remotePairingRepository) TouchLastUsed(ctx context.Context, id int64, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE remote_pairings SET last_used_at = $2 WHERE id = $1`, id, now)
	return err
}

func (r *remotePairingRepository) list(ctx context.Context, query string, args ...any) ([]service.RemotePairing, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []service.RemotePairing
	for rows.Next() {
		p, err := scanRemotePairing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func remoteAffectedOne(result sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
