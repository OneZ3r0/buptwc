package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockAndPrivateFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	lock, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if second, err := Acquire(dir); err == nil {
		second.Close()
		t.Fatal("concurrent writer acquired lock")
	}
	path := filepath.Join(dir, "session.json")
	if err := WritePrivate(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivate(path, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	b, err := ReadPrivate(path)
	if err != nil || string(b) != "replacement" {
		t.Fatalf("read: %q, %v", b, err)
	}
	for p, want := range map[string]os.FileMode{dir: 0700, path: 0600} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s: mode %o", p, info.Mode().Perm())
		}
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivate(path); err == nil {
		t.Fatal("accepted public session file")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Acquire(dir)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	next.Close()
}
