package imagecache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type UnlockFunc func() error

func (c *Cache) Lock() (UnlockFunc, error) {
	return c.LockContext(context.Background())
}

func (c *Cache) LockContext(ctx context.Context) (UnlockFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, NewError(ErrorKindUnavailable, "lock", c.config.Root, err)
	}
	if err := c.Ensure(); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(c.config.Root, ".lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, NewError(ErrorKindInternal, "lock", lockPath, err)
	}
	for {
		err = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = lockFile.Close()
			return nil, NewError(ErrorKindInternal, "lock", lockPath, err)
		}
		select {
		case <-ctx.Done():
			_ = lockFile.Close()
			return nil, NewError(ErrorKindUnavailable, "lock", lockPath, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	return func() error {
		unlockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		closeErr := lockFile.Close()
		if unlockErr != nil {
			return fmt.Errorf("unlock image cache: %w", unlockErr)
		}
		return closeErr
	}, nil
}

func (c *Cache) WithLockContext(ctx context.Context, fn func() error) (returnErr error) {
	unlock, err := c.LockContext(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := unlock(); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	return fn()
}

func (c *Cache) WithLock(fn func() error) error {
	return c.WithLockContext(context.Background(), fn)
}

func (c *Cache) TempDir(name string) (string, error) {
	if err := c.Ensure(); err != nil {
		return "", err
	}
	tmpRoot := filepath.Join(c.config.Root, "tmp")
	if err := ensureDir(tmpRoot); err != nil {
		return "", NewError(ErrorKindInternal, "tempdir", tmpRoot, err)
	}
	dir, err := os.MkdirTemp(tmpRoot, sanitizePathSegment(name)+"-*")
	if err != nil {
		return "", NewError(ErrorKindInternal, "tempdir", tmpRoot, err)
	}
	return dir, nil
}

func WriteReadyFlag(path string) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return NewError(ErrorKindInternal, "ready", path, err)
	}
	return os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
}

func ReadyFlagExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
