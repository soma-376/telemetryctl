// Package config safely updates vendor configuration while preserving originals.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Result struct {
	Path           string
	BackupPath     string
	OriginalSHA256 string
	ManagedKeys    []string
	Created        bool
}

type Backup struct {
	OriginalPath string
	BackupPath   string
	SHA256       string
	Existed      bool
}

func readFileIfExists(path string) (data []byte, existed bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// CreateBackup stores the complete original file under Pulsemetry's backup root.
func CreateBackup(path, backupRoot, tool string, at time.Time) (Backup, error) {
	raw, existed, err := readFileIfExists(path)
	if err != nil {
		return Backup{}, err
	}
	b := Backup{OriginalPath: path, Existed: existed}
	if !existed {
		return b, nil
	}
	sum := sha256.Sum256(raw)
	b.SHA256 = hex.EncodeToString(sum[:])
	ext := filepath.Ext(path)
	name := strings.TrimSuffix(filepath.Base(path), ext)
	stamp := at.UTC().Format("20060102T150405.000000000Z")
	b.BackupPath = filepath.Join(backupRoot, tool, name+"."+stamp+ext)
	if err := os.MkdirAll(filepath.Dir(b.BackupPath), 0o700); err != nil {
		return Backup{}, err
	}
	if err := os.WriteFile(b.BackupPath, raw, 0o600); err != nil {
		return Backup{}, fmt.Errorf("create backup %s: %w", b.BackupPath, err)
	}
	return b, nil
}

func RestoreBackup(backup Backup) error {
	if !backup.Existed {
		if err := os.Remove(backup.OriginalPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	raw, err := os.ReadFile(backup.BackupPath)
	if err != nil {
		return err
	}
	return AtomicWriteFile(backup.OriginalPath, raw, 0o600)
}

func AtomicWriteFile(path string, content []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pulsemetry-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
