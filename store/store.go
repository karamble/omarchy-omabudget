// Package store holds a configuration directory open and performs every read
// and write through that descriptor.
//
// The point is that a pathname is resolved once, when the directory is opened,
// and never again. A Stat followed by a ReadFile, or a CreateTemp followed by a
// Rename, resolves the same name twice and can be pointed somewhere else in
// between by anything able to move a directory or plant a link. Since these
// files hold the daemon's API token, that matters.
package store

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// Dir is an open configuration directory.
type Dir struct {
	root *os.Root
	path string
}

// Open creates the directory if needed and holds it open.
//
// os.Root keeps a descriptor on the directory, so later operations act on the
// directory that was opened even if something replaces the one at this path.
func Open(path string) (*Dir, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", path, err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return &Dir{root: root, path: path}, nil
}

// Path reports the directory this was opened from.
func (d *Dir) Path() string { return d.path }

// Close releases the descriptor.
func (d *Dir) Close() error { return d.root.Close() }

// ErrTooOpen reports a file whose permissions are wider than requested.
var ErrTooOpen = errors.New("file is readable by others")

// Read returns the contents of name, refusing to follow a symlink in its final
// component and refusing a file whose mode is wider than want.
//
// The mode is taken from the open descriptor rather than from a second lookup,
// so what was checked and what was read cannot differ.
func (d *Dir) Read(name string, want fs.FileMode) ([]byte, error) {
	f, err := d.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if perm := info.Mode().Perm(); perm&^want != 0 {
		return nil, fmt.Errorf("%s is mode %04o, want %04o: %w",
			filepath.Join(d.path, name), perm, want, ErrTooOpen)
	}

	return io.ReadAll(f)
}

// tempPrefix marks a file as a half-written replacement. Sweep removes them.
const tempPrefix = ".tmp-"

// Write replaces name atomically: a fresh file inside the same directory, then
// a rename through the same descriptor.
//
// Nothing here takes a path. The temporary file is created, written, synced and
// renamed through the open directory, so the replacement cannot be redirected
// after the fact.
func (d *Dir) Write(name string, data []byte, perm fs.FileMode) (err error) {
	tmp, err := d.tempName()
	if err != nil {
		return err
	}

	f, err := d.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", d.path, err)
	}
	defer func() {
		if err != nil {
			_ = d.root.Remove(tmp)
		}
	}()

	if _, err = f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	// Sync before the rename, or a crash can leave the new name pointing at a
	// file whose contents never reached the disk.
	if err = f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("syncing %s: %w", tmp, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp, err)
	}
	if err = d.root.Rename(tmp, name); err != nil {
		return fmt.Errorf("replacing %s: %w", name, err)
	}
	return nil
}

func (d *Dir) tempName() (string, error) {
	b := make([]byte, 9)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("naming a temporary file: %w", err)
	}
	return tempPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// Exists reports whether name is present, without following a link out of the
// directory.
func (d *Dir) Exists(name string) (bool, error) {
	_, err := d.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Remove deletes name.
func (d *Dir) Remove(name string) error { return d.root.Remove(name) }

// Sweep deletes leftover temporary files and reports how many went.
//
// A write interrupted between creating the temporary file and renaming it
// leaves one behind, and a daemon killed often enough accumulates them: five
// were sitting in the config directory when this was written. Callers run this
// at startup.
//
// Legacy names from the previous CreateTemp-based writers are swept too, so the
// existing litter is cleared rather than left forever.
func (d *Dir) Sweep(legacyPrefixes ...string) (int, error) {
	dir, err := d.root.Open(".")
	if err != nil {
		return 0, err
	}
	defer dir.Close()

	entries, err := dir.ReadDir(-1)
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		match := strings.HasPrefix(name, tempPrefix)
		for _, p := range legacyPrefixes {
			if strings.HasPrefix(name, p) {
				match = true
			}
		}
		if !match {
			continue
		}
		if err := d.root.Remove(name); err == nil {
			removed++
		}
	}
	return removed, nil
}

// shared caches one Dir per directory for the life of the process.
//
// Opening the directory once and keeping the descriptor is the whole point: a
// Dir reopened per call would resolve the pathname again every time, which is
// the pattern this package exists to remove. The daemon has exactly one
// configuration directory and runs for weeks, so one entry is the normal case.
var shared struct {
	sync.Mutex
	dirs map[string]*Dir
}

// Shared returns the Dir for path, opening it the first time and reusing the
// open descriptor afterwards.
func Shared(path string) (*Dir, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	shared.Lock()
	defer shared.Unlock()
	if d, ok := shared.dirs[abs]; ok {
		return d, nil
	}
	d, err := Open(abs)
	if err != nil {
		return nil, err
	}
	if shared.dirs == nil {
		shared.dirs = make(map[string]*Dir)
	}
	shared.dirs[abs] = d
	return d, nil
}
