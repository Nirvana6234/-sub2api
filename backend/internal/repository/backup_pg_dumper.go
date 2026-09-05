package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	systemConfigBackupFormat   = "sub2api-system-config-v1"
	maxSystemConfigBackupBytes = 16 << 20
)

// PgDumper implements service.DBDumper with a narrow, data-only configuration
// archive. It never invokes pg_dump --clean or exports database schema.
type PgDumper struct {
	cfg *config.DatabaseConfig
}

type systemConfigSnapshot struct {
	Format                 string            `json:"format"`
	Settings               []systemConfigKV  `json:"settings"`
	ErrorPassthroughRules  []json.RawMessage `json:"error_passthrough_rules"`
	TLSFingerprintProfiles []json.RawMessage `json:"tls_fingerprint_profiles"`
}

type systemConfigKV struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewPgDumper creates a new PgDumper.
func NewPgDumper(cfg *config.Config) service.DBDumper {
	return &PgDumper{cfg: &cfg.Database}
}

// systemConfigSnapshotQuery reads only the explicitly approved configuration
// records. It emits one JSON document so restore can validate it and generate
// its own fixed SQL rather than executing SQL supplied by the archive.
func systemConfigSnapshotQuery(settingKeys []string) string {
	quotedKeys := make([]string, 0, len(settingKeys))
	for _, key := range settingKeys {
		quotedKeys = append(quotedKeys, sqlStringLiteral(key))
	}

	return fmt.Sprintf(`
SELECT json_build_object(
    'format', %s,
    'settings', COALESCE(
        (
            SELECT json_agg(
                json_build_object('key', setting.key, 'value', setting.value, 'updated_at', setting.updated_at)
                ORDER BY setting.key
            )
            FROM public.settings AS setting
            WHERE setting.key IN (%s)
        ),
        '[]'::json
    ),
    'error_passthrough_rules', COALESCE(
        (
            SELECT json_agg(row_to_json(rule) ORDER BY rule.id)
            FROM public.error_passthrough_rules AS rule
        ),
        '[]'::json
    ),
    'tls_fingerprint_profiles', COALESCE(
        (
            SELECT json_agg(row_to_json(profile) ORDER BY profile.id)
            FROM public.tls_fingerprint_profiles AS profile
        ),
        '[]'::json
    )
)::text;
`, sqlStringLiteral(systemConfigBackupFormat), strings.Join(quotedKeys, ", "))
}

func sqlStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func psqlArgs(cfg *config.DatabaseConfig) []string {
	return []string{
		"-X",
		"-h", cfg.Host,
		"-p", fmt.Sprintf("%d", cfg.Port),
		"-U", cfg.User,
		"-d", cfg.DBName,
	}
}

func (d *PgDumper) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "psql", args...)
	if d.cfg.Password != "" {
		cmd.Env = append(cmd.Environ(), "PGPASSWORD="+d.cfg.Password)
	}
	if d.cfg.SSLMode != "" {
		cmd.Env = append(cmd.Environ(), "PGSSLMODE="+d.cfg.SSLMode)
	}
	return cmd
}

// DumpSystemConfig writes only the reviewed system-configuration allow-list as
// a JSON snapshot. There is intentionally no schema dump and no table
// exclusion fallback: a new table cannot enter backups accidentally.
func (d *PgDumper) DumpSystemConfig(ctx context.Context) (io.ReadCloser, error) {
	args := append(psqlArgs(d.cfg), "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", systemConfigSnapshotQuery(service.SystemConfigBackupSettingKeys()))
	cmd := d.command(ctx, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start psql system config dump: %w", err)
	}

	return &cmdReadCloser{ReadCloser: stdout, cmd: cmd}, nil
}

// Restore validates the JSON archive and generates a fixed transaction that
// can touch only the reviewed configuration tables and allow-listed settings.
// This also rejects legacy pg_dump archives that could contain schema or user
// data operations.
func (d *PgDumper) Restore(ctx context.Context, data io.Reader) error {
	snapshot, err := readSystemConfigSnapshot(data)
	if err != nil {
		return err
	}

	script, err := snapshot.restoreSQL()
	if err != nil {
		return err
	}

	args := append(psqlArgs(d.cfg), "-v", "ON_ERROR_STOP=1")
	cmd := d.command(ctx, args...)
	cmd.Stdin = strings.NewReader(script)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, string(output))
	}
	return nil
}

func readSystemConfigSnapshot(data io.Reader) (*systemConfigSnapshot, error) {
	content, err := io.ReadAll(io.LimitReader(data, maxSystemConfigBackupBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read system config backup: %w", err)
	}
	if len(content) > maxSystemConfigBackupBytes {
		return nil, fmt.Errorf("system config backup exceeds %d byte limit", maxSystemConfigBackupBytes)
	}

	var snapshot systemConfigSnapshot
	if err := json.Unmarshal(content, &snapshot); err != nil {
		return nil, fmt.Errorf("decode system config backup: %w", err)
	}
	if snapshot.Format != systemConfigBackupFormat {
		return nil, fmt.Errorf("unsupported system config backup format %q", snapshot.Format)
	}
	if err := snapshot.validate(); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (s *systemConfigSnapshot) validate() error {
	allowedSettings := make(map[string]struct{}, len(service.SystemConfigBackupSettingKeys()))
	for _, key := range service.SystemConfigBackupSettingKeys() {
		allowedSettings[key] = struct{}{}
	}
	seenSettings := make(map[string]struct{}, len(s.Settings))
	for _, setting := range s.Settings {
		if _, ok := allowedSettings[setting.Key]; !ok {
			return fmt.Errorf("system config backup contains unapproved setting %q", setting.Key)
		}
		if _, exists := seenSettings[setting.Key]; exists {
			return fmt.Errorf("system config backup contains duplicate setting %q", setting.Key)
		}
		seenSettings[setting.Key] = struct{}{}
	}
	for _, row := range append(append([]json.RawMessage{}, s.ErrorPassthroughRules...), s.TLSFingerprintProfiles...) {
		if !json.Valid(row) || len(row) == 0 || row[0] != '{' {
			return fmt.Errorf("system config backup contains an invalid rule row")
		}
	}
	return nil
}

func (s *systemConfigSnapshot) restoreSQL() (string, error) {
	if err := s.validate(); err != nil {
		return "", err
	}

	var script strings.Builder
	script.WriteString("BEGIN;\n")
	script.WriteString("DELETE FROM public.error_passthrough_rules;\n")
	script.WriteString("DELETE FROM public.tls_fingerprint_profiles;\n")
	for _, row := range s.ErrorPassthroughRules {
		script.WriteString("INSERT INTO public.error_passthrough_rules SELECT (json_populate_record(NULL::public.error_passthrough_rules, ")
		script.WriteString(sqlStringLiteral(string(row)))
		script.WriteString("::json)).*;\n")
	}
	for _, row := range s.TLSFingerprintProfiles {
		script.WriteString("INSERT INTO public.tls_fingerprint_profiles SELECT (json_populate_record(NULL::public.tls_fingerprint_profiles, ")
		script.WriteString(sqlStringLiteral(string(row)))
		script.WriteString("::json)).*;\n")
	}
	script.WriteString("SELECT setval(pg_get_serial_sequence('public.error_passthrough_rules', 'id'), COALESCE((SELECT MAX(id) FROM public.error_passthrough_rules), 1), (SELECT COUNT(*) > 0 FROM public.error_passthrough_rules));\n")
	script.WriteString("SELECT setval(pg_get_serial_sequence('public.tls_fingerprint_profiles', 'id'), COALESCE((SELECT MAX(id) FROM public.tls_fingerprint_profiles), 1), (SELECT COUNT(*) > 0 FROM public.tls_fingerprint_profiles));\n")
	for _, setting := range s.Settings {
		script.WriteString("INSERT INTO public.settings (key, value, updated_at) VALUES (")
		script.WriteString(sqlStringLiteral(setting.Key))
		script.WriteString(", ")
		script.WriteString(sqlStringLiteral(setting.Value))
		script.WriteString(", ")
		script.WriteString(sqlStringLiteral(setting.UpdatedAt.UTC().Format(time.RFC3339Nano)))
		script.WriteString("::timestamptz) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at;\n")
	}
	script.WriteString("COMMIT;\n")
	return script.String(), nil
}

// cmdReadCloser wraps a command stdout pipe and waits for the process on Close.
type cmdReadCloser struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (c *cmdReadCloser) Close() error {
	_ = c.ReadCloser.Close()
	if err := c.cmd.Wait(); err != nil {
		return fmt.Errorf("psql exited with error: %w", err)
	}
	return nil
}
