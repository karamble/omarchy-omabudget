package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteThenRead(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "cfg"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	want := []byte(`{"token":"secret"}`)
	if err := d.Write("sites.json", want, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := d.Read("sites.json", 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("read %q, wrote %q", got, want)
	}

	info, err := os.Stat(filepath.Join(d.Path(), "sites.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %04o, want 0600", perm)
	}
}

// TestWriteLeavesNoTemp is the defect that left five orphans in the real config
// directory: a successful write must not leave its temporary file behind.
func TestWriteLeavesNoTemp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	for i := 0; i < 5; i++ {
		if err := d.Write("state.json", []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("after five writes the directory holds %v, want only state.json", names)
	}
}

func TestSweepRemovesLegacyTemps(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// The exact shapes found in ~/.config/sitesentinel.
	for _, name := range []string{
		".state-1472528310.json", ".state-937282217.json",
		".sites-abc.json", ".tmp-leftover",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Write("state.json", []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	n, err := d.Sweep(".state-", ".sites-", ".triggers-")
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("swept %d, want 4", n)
	}
	if ok, _ := d.Exists("state.json"); !ok {
		t.Error("sweep removed the real file")
	}
}

// TestReadRefusesWideMode keeps the permission check that guards the API token.
func TestReadRefusesWideMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if err := os.WriteFile(filepath.Join(dir, "sites.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = d.Read("sites.json", 0o600)
	if !errors.Is(err, ErrTooOpen) {
		t.Errorf("err = %v, want ErrTooOpen", err)
	}
}

// TestReadRefusesSymlink is the traversal the review asked about: a link planted
// where the store lives must not redirect the read, even though it is reachable
// through the directory.
func TestReadRefusesSymlink(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "cfg")
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	secret := filepath.Join(base, "elsewhere.json")
	if err := os.WriteFile(secret, []byte(`{"stolen":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "sites.json")); err != nil {
		t.Fatal(err)
	}

	if _, err := d.Read("sites.json", 0o600); err == nil {
		t.Fatal("read followed a symlink out of the directory")
	}
}

// TestWriteSurvivesDirectorySwap is the retained-identity property: once the
// directory is open, replacing what sits at that path must not redirect writes.
func TestWriteSurvivesDirectorySwap(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "cfg")
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	decoy := filepath.Join(base, "decoy")
	if err := os.MkdirAll(decoy, 0o700); err != nil {
		t.Fatal(err)
	}
	// Swap the directory out from under the path after it was opened.
	if err := os.Rename(dir, filepath.Join(base, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(decoy, dir); err != nil {
		t.Fatal(err)
	}

	if err := d.Write("sites.json", []byte(`{"token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// The write must have landed in the directory that was opened, not in
	// whatever now answers to that path.
	if _, err := os.Stat(filepath.Join(dir, "sites.json")); err == nil {
		t.Error("write landed in the directory that replaced the original")
	}
	got, err := os.ReadFile(filepath.Join(base, "moved", "sites.json"))
	if err != nil {
		t.Fatalf("write did not land in the opened directory: %v", err)
	}
	if !strings.Contains(string(got), "secret") {
		t.Errorf("contents = %q", got)
	}
}

func TestReadMissing(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "cfg"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if _, err := d.Read("absent.json", 0o600); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist", err)
	}
}
