package orgs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/ownscalenet/ownscale/derrors"
)

// ErrDataDirLocked is returned by LockDataDir while another process, such
// as the running ownscale server, holds the lock.
var ErrDataDirLocked = errors.New("data directory is in use by a running ownscale")

// LockDataDir takes an exclusive lock on dataDir until unlock is called or
// the process exits. ownscale holds it while running, so offline commands
// can tell it's stopped even when every organization sleeps.
func LockDataDir(dataDir string) (unlock func() error, err error) {
	defer derrors.Wrap(&err, "LockDataDir(%q)", dataDir)

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dataDir, "ownscale.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrDataDirLocked
		}
		return nil, err
	}
	return f.Close, nil
}
