package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type fileSnapshot struct {
	existed bool
	mode    os.FileMode
	data    []byte
}

type installTransaction struct {
	installDir      string
	dataDir         string
	installExisted  bool
	dataExisted     bool
	previousInstall bool
	upAttempted     bool
	snapshots       map[string]fileSnapshot
	committed       bool
}

func beginInstallTransaction(installDir, dataDir string) (*installTransaction, error) {
	_, installErr := os.Stat(installDir)
	_, dataErr := os.Stat(dataDir)
	tx := &installTransaction{
		installDir: installDir, dataDir: dataDir,
		installExisted: installErr == nil, dataExisted: dataErr == nil,
		snapshots: map[string]fileSnapshot{},
	}
	for _, name := range managedFiles {
		path := filepath.Join(installDir, name)
		data, exists, err := readOptionalFile(path)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", path, err)
		}
		snapshot := fileSnapshot{existed: exists, data: data}
		if exists {
			info, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			snapshot.mode = info.Mode().Perm()
		}
		tx.snapshots[name] = snapshot
	}
	tx.previousInstall = tx.snapshots["docker-compose.yml"].existed && tx.snapshots[".env"].existed
	return tx, nil
}

func (t *installTransaction) Commit() { t.committed = true }

func (t *installTransaction) Rollback() error {
	if t.committed {
		return nil
	}
	var rollbackErr error
	for _, name := range managedFiles {
		path := filepath.Join(t.installDir, name)
		snapshot := t.snapshots[name]
		if !snapshot.existed {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				rollbackErr = errors.Join(rollbackErr, err)
			}
			continue
		}
		if err := os.WriteFile(path, snapshot.data, snapshot.mode); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
			continue
		}
		if err := os.Chmod(path, snapshot.mode); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	if !t.dataExisted {
		if err := os.RemoveAll(t.dataDir); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	if !t.installExisted {
		if err := os.Remove(t.installDir); err != nil && !os.IsNotExist(err) {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	return rollbackErr
}
