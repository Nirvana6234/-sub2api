package service

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

func (s *BackupService) createCompressedBackupFile(ctx context.Context) (string, int64, error) {
	dumpReader, err := s.dumper.DumpSystemConfig(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("system config snapshot: %w", err)
	}
	archive, err := os.CreateTemp("", "sub2api-backup-*.json.gz")
	if err != nil {
		_ = dumpReader.Close()
		return "", 0, fmt.Errorf("create backup archive: %w", err)
	}
	path := archive.Name()
	gz := gzip.NewWriter(archive)
	_, copyErr := io.Copy(gz, dumpReader)
	if e := gz.Close(); copyErr == nil {
		copyErr = e
	}
	if e := dumpReader.Close(); copyErr == nil {
		copyErr = e
	}
	if e := archive.Close(); copyErr == nil {
		copyErr = e
	}
	if copyErr != nil {
		_ = cleanupBackupFiles(path)
		return "", 0, fmt.Errorf("gzip/dump failed: %w", copyErr)
	}
	info, err := os.Stat(path)
	if err != nil {
		_ = cleanupBackupFiles(path)
		return "", 0, fmt.Errorf("stat backup archive: %w", err)
	}
	return path, info.Size(), nil
}

func (s *BackupService) uploadBackupArchive(ctx context.Context, record *BackupRecord, store BackupObjectStore, cfg *BackupS3Config, archivePath string) error {
	info, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("stat backup archive: %w", err)
	}
	partSize := s.partSizeBytes
	if partSize <= 0 {
		partSize = defaultBackupPartSizeBytes
	}
	uploadFile := func(key, path, contentType string) (int64, error) {
		f, err := os.Open(path)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		return store.Upload(ctx, key, f, contentType)
	}
	if info.Size() <= partSize {
		if _, err := uploadFile(record.S3Key, archivePath, "application/gzip"); err != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			cleanupErr := deleteBackupObjectKeys(cleanupCtx, store, record)
			cleanupCancel()
			return errors.Join(fmt.Errorf("backup upload: %w", err), cleanupErr)
		}
		record.Parts = nil
		return nil
	}
	if cfg == nil {
		return errors.New("backup S3 config is unavailable for split upload")
	}
	localParts, err := splitBackupFile(archivePath, partSize)
	if err != nil {
		return fmt.Errorf("split backup archive: %w", err)
	}
	defer func() {
		for _, p := range localParts {
			_ = cleanupBackupFiles(p.Path)
		}
	}()
	record.S3Key = ""
	record.Parts = make([]BackupPart, 0, len(localParts))
	root := strings.TrimRight(s.buildS3Key(cfg, record.ID), "/")
	for _, p := range localParts {
		record.Parts = append(record.Parts, BackupPart{Index: p.Index, S3Key: s.buildBackupPartKey(root, p.Index), SizeBytes: p.SizeBytes, SHA256: p.SHA256})
	}
	if err := s.saveRecord(ctx, record); err != nil {
		return fmt.Errorf("save split backup plan: %w", err)
	}
	for _, p := range record.Parts {
		var local localBackupPart
		for _, candidate := range localParts {
			if candidate.Index == p.Index {
				local = candidate
				break
			}
		}
		if _, err := uploadFile(p.S3Key, local.Path, "application/octet-stream"); err != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			cleanupErr := deleteBackupObjectKeys(cleanupCtx, store, record)
			cleanupCancel()
			return errors.Join(fmt.Errorf("upload backup part %d: %w", p.Index, err), cleanupErr)
		}
	}
	return nil
}

func (s *BackupService) buildBackupPartKey(root string, index int) string {
	return fmt.Sprintf("%s/payload.part-%06d", strings.TrimRight(root, "/"), index)
}

func (s *BackupService) downloadBackupParts(ctx context.Context, store BackupObjectStore, parts []BackupPart) (string, error) {
	if len(parts) == 0 {
		return "", errors.New("backup parts are empty")
	}
	ordered := append([]BackupPart(nil), parts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Index < ordered[j].Index })
	for i, p := range ordered {
		if p.Index != i+1 || p.S3Key == "" || p.SizeBytes <= 0 {
			return "", fmt.Errorf("invalid backup part metadata at index %d", i+1)
		}
	}
	f, err := os.CreateTemp("", "sub2api-restore-*.json.gz")
	if err != nil {
		return "", fmt.Errorf("create restore archive: %w", err)
	}
	path := f.Name()
	cleanup := func() { _ = f.Close(); _ = cleanupBackupFiles(path) }
	for _, p := range ordered {
		body, err := store.Download(ctx, p.S3Key)
		if err != nil {
			cleanup()
			return "", fmt.Errorf("download backup part %d: %w", p.Index, err)
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(f, h), body)
		closeErr := body.Close()
		if copyErr != nil || closeErr != nil {
			cleanup()
			return "", fmt.Errorf("read backup part %d: %w", p.Index, errors.Join(copyErr, closeErr))
		}
		if n != p.SizeBytes || (p.SHA256 != "" && !strings.EqualFold(p.SHA256, hex.EncodeToString(h.Sum(nil)))) {
			cleanup()
			return "", fmt.Errorf("backup part %d checksum or size mismatch", p.Index)
		}
	}
	if err := f.Close(); err != nil {
		_ = cleanupBackupFiles(path)
		return "", fmt.Errorf("close restore archive: %w", err)
	}
	return path, nil
}

func (s *BackupService) restoreArchive(ctx context.Context, archivePath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open restore archive: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()
	if err := s.dumper.Restore(ctx, gz); err != nil {
		return fmt.Errorf("restore system config: %w", err)
	}
	return nil
}

func backupObjectKeys(record *BackupRecord) []string {
	if record == nil {
		return nil
	}
	seen := map[string]struct{}{}
	keys := []string{}
	add := func(k string) {
		if k != "" {
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				keys = append(keys, k)
			}
		}
	}
	add(record.S3Key)
	parts := append([]BackupPart(nil), record.Parts...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].Index < parts[j].Index })
	for _, p := range parts {
		add(p.S3Key)
	}
	return keys
}

func (s *BackupService) deleteBackupObjects(ctx context.Context, record *BackupRecord) error {
	if len(backupObjectKeys(record)) == 0 {
		return nil
	}
	cfg, err := s.loadS3Config(ctx)
	if err != nil {
		return err
	}
	if cfg == nil || !cfg.IsConfigured() {
		return nil
	}
	store, err := s.getOrCreateStore(ctx, cfg)
	if err != nil {
		return err
	}
	return deleteBackupObjectKeys(ctx, store, record)
}

func deleteBackupObjectKeys(ctx context.Context, store BackupObjectStore, record *BackupRecord) error {
	var errs []error
	for _, key := range backupObjectKeys(record) {
		if err := store.Delete(ctx, key); err != nil {
			errs = append(errs, fmt.Errorf("delete backup object %q: %w", key, err))
		}
	}
	return errors.Join(errs...)
}

func (s *BackupService) cleanupStaleBackupObjects(record *BackupRecord) error {
	if len(backupObjectKeys(record)) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return s.deleteBackupObjects(ctx, record)
}

func (s *BackupService) saveRecoveredRecord(record *BackupRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.saveRecord(ctx, record); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] save recovered record %s: %v", record.ID, err)
	}
}
