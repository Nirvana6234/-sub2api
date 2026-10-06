package repository

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/lib/pq"
)

// relayNodeRepository 是主从分流节点、证书、审计的存储（原始 SQL，migrations/259_relay_nodes.sql）。
// 状态变更都是带当前状态条件的 UPDATE，并发的管理操作不会互相覆盖。
type relayNodeRepository struct {
	db *sql.DB
}

// NewRelayNodeRepository 创建节点存储。
func NewRelayNodeRepository(db *sql.DB) master.NodeStore {
	return &relayNodeRepository{db: db}
}

const relayNodeColumns = `id, name, hostname, region, COALESCE(public_domain, ''), status, identity_public_key,
identity_fingerprint, encryption_public_key, registered_ip, program_version, system_info, bandwidth_limit_mbps,
allow_multi_ip, activated_at, activated_by, last_seen_at, last_seen_ip, created_at, updated_at`

func scanRelayNode(row rowScanner) (*master.Node, error) {
	var n master.Node
	var status, identityKey, encKey string
	var sysInfo []byte
	var activatedAt, lastSeen sql.NullTime
	var activatedBy sql.NullInt64
	err := row.Scan(&n.ID, &n.Name, &n.Hostname, &n.Region, &n.PublicDomain, &status, &identityKey,
		&n.IdentityFingerprint, &encKey, &n.RegisteredIP, &n.ProgramVersion, &sysInfo, &n.BandwidthLimitMbps,
		&n.AllowMultiIP, &activatedAt, &activatedBy, &lastSeen, &n.LastSeenIP, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, master.ErrNodeNotFound
	}
	if err != nil {
		return nil, err
	}
	n.Status = master.NodeStatus(status)
	n.IdentityPublicKey = decodeRelayKey(identityKey)
	n.EncryptionPublicKey = decodeRelayKey(encKey)
	if len(sysInfo) > 0 {
		_ = json.Unmarshal(sysInfo, &n.SystemInfo)
	}
	if activatedAt.Valid {
		t := activatedAt.Time
		n.ActivatedAt = &t
	}
	if activatedBy.Valid {
		v := activatedBy.Int64
		n.ActivatedBy = &v
	}
	if lastSeen.Valid {
		t := lastSeen.Time
		n.LastSeenAt = &t
	}
	return &n, nil
}

// 公钥以十六进制存进 TEXT 列，便于在库里直接查看和比对。
func encodeRelayKey(b []byte) string { return hex.EncodeToString(b) }

func decodeRelayKey(s string) []byte {
	if s == "" {
		return nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

func (r *relayNodeRepository) CreatePending(ctx context.Context, n *master.Node, maxPending int) (*master.Node, error) {
	sysInfo, err := json.Marshal(n.SystemInfo)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// 串行化并发注册：同一事务里数待激活数量再插入。
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('relay_nodes_register'))`); err != nil {
		return nil, err
	}
	existing, err := scanRelayNode(tx.QueryRowContext(ctx,
		`SELECT `+relayNodeColumns+` FROM relay_nodes WHERE identity_fingerprint = $1 AND deleted_at IS NULL`, n.IdentityFingerprint))
	if err == nil {
		return existing, tx.Commit()
	}
	if !errors.Is(err, master.ErrNodeNotFound) {
		return nil, err
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM relay_nodes WHERE status = 'pending' AND deleted_at IS NULL`).Scan(&pending); err != nil {
		return nil, err
	}
	if pending >= maxPending {
		return nil, master.ErrPendingLimit
	}
	created, err := scanRelayNode(tx.QueryRowContext(ctx, `
		INSERT INTO relay_nodes (name, hostname, status, identity_public_key, identity_fingerprint, registered_ip, program_version, system_info)
		VALUES ($1, $2, 'pending', $3, $4, $5, $6, $7)
		RETURNING `+relayNodeColumns,
		n.Name, n.Hostname, encodeRelayKey(n.IdentityPublicKey), n.IdentityFingerprint, n.RegisteredIP, n.ProgramVersion, sysInfo))
	if err != nil {
		return nil, err
	}
	return created, tx.Commit()
}

func (r *relayNodeRepository) UpdateRegistration(ctx context.Context, id int64, hostname, programVersion, displayName string, systemInfo map[string]string, ip string) error {
	sysInfo, err := json.Marshal(systemInfo)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE relay_nodes SET hostname = $2, program_version = $3,
			name = CASE WHEN $4 = '' THEN name ELSE $4 END,
			system_info = $5, registered_ip = $6, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`, id, hostname, programVersion, displayName, sysInfo, ip)
	return requireRelayRow(res, err)
}

func (r *relayNodeRepository) GetByID(ctx context.Context, id int64) (*master.Node, error) {
	return scanRelayNode(r.db.QueryRowContext(ctx, `SELECT `+relayNodeColumns+` FROM relay_nodes WHERE id = $1 AND deleted_at IS NULL`, id))
}

func (r *relayNodeRepository) GetByFingerprint(ctx context.Context, fp string) (*master.Node, error) {
	return scanRelayNode(r.db.QueryRowContext(ctx,
		`SELECT `+relayNodeColumns+` FROM relay_nodes WHERE identity_fingerprint = $1 AND deleted_at IS NULL`, fp))
}

func (r *relayNodeRepository) List(ctx context.Context) ([]*master.Node, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+relayNodeColumns+` FROM relay_nodes WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*master.Node
	for rows.Next() {
		n, err := scanRelayNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r *relayNodeRepository) SetStatus(ctx context.Context, id int64, from []master.NodeStatus, to master.NodeStatus) (bool, error) {
	statuses := make([]string, len(from))
	for i, s := range from {
		statuses[i] = string(s)
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE relay_nodes SET status = $2, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL AND status = ANY($3)`, id, string(to), pq.Array(statuses))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		if _, err := r.GetByID(ctx, id); err != nil {
			return false, err
		}
	}
	return n > 0, nil
}

func (r *relayNodeRepository) Activate(ctx context.Context, id int64, a master.Activation) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE relay_nodes SET status = 'active',
			name = CASE WHEN $2 = '' THEN name ELSE $2 END,
			public_domain = $3, bandwidth_limit_mbps = $4, region = $5,
			activated_at = $6, activated_by = $7, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL AND status = 'pending'`,
		id, a.Name, a.PublicDomain, a.BandwidthLimitMbps, a.Region, a.At, nullableActor(a.ActorUserID))
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return master.ErrDomainTaken
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		if _, err := r.GetByID(ctx, id); err != nil {
			return err
		}
		return master.ErrStatusConflict
	}
	return nil
}

func (r *relayNodeRepository) ReplaceNode(ctx context.Context, fromID, toID int64, a master.Activation) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// 先清旧节点的域名（域名全局唯一），再激活新节点；任何一步不符合就整个回滚。
	res, err := tx.ExecContext(ctx, `
		UPDATE relay_nodes SET public_domain = NULL, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL AND status = 'disabled'`, fromID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		if _, err := r.GetByID(ctx, fromID); err != nil {
			return err
		}
		return master.ErrStatusConflict
	}
	res, err = tx.ExecContext(ctx, `
		UPDATE relay_nodes SET status = 'active',
			name = CASE WHEN $2 = '' THEN name ELSE $2 END,
			public_domain = $3, bandwidth_limit_mbps = $4, region = $5,
			activated_at = $6, activated_by = $7, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL AND status = 'pending'`,
		toID, a.Name, a.PublicDomain, a.BandwidthLimitMbps, a.Region, a.At, nullableActor(a.ActorUserID))
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return master.ErrDomainTaken
		}
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		if _, err := r.GetByID(ctx, toID); err != nil {
			return err
		}
		return master.ErrStatusConflict
	}
	return tx.Commit()
}

func (r *relayNodeRepository) SetAllowMultiIP(ctx context.Context, id int64, allow bool) error {
	res, err := r.db.ExecContext(ctx, `UPDATE relay_nodes SET allow_multi_ip = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, id, allow)
	return requireRelayRow(res, err)
}

func (r *relayNodeRepository) SetEncryptionKey(ctx context.Context, id int64, key []byte) error {
	res, err := r.db.ExecContext(ctx, `UPDATE relay_nodes SET encryption_public_key = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, id, encodeRelayKey(key))
	return requireRelayRow(res, err)
}

func (r *relayNodeRepository) TouchSeen(ctx context.Context, id int64, ip string, at time.Time) (bool, error) {
	var changed bool
	err := r.db.QueryRowContext(ctx, `
		UPDATE relay_nodes n SET last_seen_at = $3, last_seen_ip = $2,
			last_ip_changed_at = CASE WHEN n.last_seen_ip <> '' AND n.last_seen_ip <> $2 THEN $3 ELSE n.last_ip_changed_at END
		FROM (SELECT id, last_seen_ip AS prev_ip FROM relay_nodes WHERE id = $1 AND deleted_at IS NULL FOR UPDATE) p
		WHERE n.id = p.id
		RETURNING p.prev_ip <> '' AND p.prev_ip <> $2`, id, ip, at).Scan(&changed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, master.ErrNodeNotFound
	}
	return changed, err
}

func (r *relayNodeRepository) PurgeStalePending(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE relay_nodes SET deleted_at = NOW(), updated_at = NOW()
		WHERE status = 'pending' AND activated_at IS NULL AND created_at < $1 AND deleted_at IS NULL`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *relayNodeRepository) InsertCertificate(ctx context.Context, c *master.Certificate) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO relay_node_certificates (node_id, serial, public_key, certificate_der, not_before, not_after, renewed_from_serial)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		c.NodeID, c.Serial, encodeRelayKey(c.PublicKey), c.DER, c.NotBefore, c.NotAfter, c.RenewedFromSerial)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" && strings.Contains(pqErr.Constraint, "renewed_from") {
		return master.ErrDuplicateRenewal
	}
	return err
}

const relayCertColumns = `id, node_id, serial, public_key, certificate_der, not_before, not_after, renewed_from_serial, revoked_at, revoke_reason, created_at`

func scanRelayCert(row rowScanner) (*master.Certificate, error) {
	var c master.Certificate
	var pub string
	var revoked sql.NullTime
	err := row.Scan(&c.ID, &c.NodeID, &c.Serial, &pub, &c.DER, &c.NotBefore, &c.NotAfter, &c.RenewedFromSerial, &revoked, &c.RevokeReason, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, master.ErrNodeNotFound
	}
	if err != nil {
		return nil, err
	}
	c.PublicKey = decodeRelayKey(pub)
	if revoked.Valid {
		t := revoked.Time
		c.RevokedAt = &t
	}
	return &c, nil
}

func (r *relayNodeRepository) GetCertificate(ctx context.Context, serial string) (*master.Certificate, error) {
	return scanRelayCert(r.db.QueryRowContext(ctx, `SELECT `+relayCertColumns+` FROM relay_node_certificates WHERE serial = $1`, serial))
}

func (r *relayNodeRepository) GetRenewalOf(ctx context.Context, serial string) (*master.Certificate, error) {
	return scanRelayCert(r.db.QueryRowContext(ctx,
		`SELECT `+relayCertColumns+` FROM relay_node_certificates WHERE renewed_from_serial = $1 AND renewed_from_serial <> ''`, serial))
}

func (r *relayNodeRepository) ListRenewedSerials(ctx context.Context, notAfter time.Time) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.serial FROM relay_node_certificates c
		WHERE c.not_after > $1
		  AND EXISTS (SELECT 1 FROM relay_node_certificates r WHERE r.renewed_from_serial = c.serial)
		ORDER BY c.serial`, notAfter)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *relayNodeRepository) CountCertificates(ctx context.Context, nodeID int64) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM relay_node_certificates WHERE node_id = $1`, nodeID).Scan(&n)
	return n, err
}

func (r *relayNodeRepository) RevokeCertificates(ctx context.Context, nodeID int64, reason string, at time.Time) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		UPDATE relay_node_certificates SET revoked_at = $3, revoke_reason = $2
		WHERE node_id = $1 AND revoked_at IS NULL AND not_after > $3
		RETURNING serial`, nodeID, reason, at)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *relayNodeRepository) LastSuspectRevocation(ctx context.Context, nodeID int64) (time.Time, bool, error) {
	var last sql.NullTime
	err := r.db.QueryRowContext(ctx, `SELECT MAX(revoked_at) FROM relay_node_certificates
		WHERE node_id = $1 AND revoked_at IS NOT NULL AND (revoke_reason = 'identity_duplicated' OR revoke_reason LIKE 'revoked:%')`, nodeID).Scan(&last)
	if err != nil {
		return time.Time{}, false, err
	}
	return last.Time, last.Valid, nil
}

func (r *relayNodeRepository) ListRevokedSerials(ctx context.Context, notAfter time.Time) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT serial FROM relay_node_certificates WHERE revoked_at IS NOT NULL AND not_after > $1 ORDER BY serial`, notAfter)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *relayNodeRepository) Audit(ctx context.Context, e master.AuditEntry) error {
	detail, err := json.Marshal(e.Detail)
	if err != nil {
		return err
	}
	if e.Detail == nil {
		detail = []byte("{}")
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO relay_node_audit_logs (node_id, actor_user_id, action, source_ip, detail)
		VALUES ($1, $2, $3, $4, $5)`,
		nullableID(e.NodeID), nullableActor(e.ActorUserID), e.Action, e.SourceIP, detail)
	return err
}

func requireRelayRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return master.ErrNodeNotFound
	}
	return nil
}

func nullableID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

func nullableActor(id int64) any { return nullableID(id) }
