package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := DefaultPath()
	if err != nil || got != filepath.Join(dir, "buptwc", "config.toml") {
		t.Fatalf("%s %v", got, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if _, err := DefaultPath(); err == nil {
		t.Fatal("relative XDG directory accepted")
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err = DefaultPath()
	if err != nil || got != filepath.Join(home, ".config", "buptwc", "config.toml") {
		t.Fatalf("fallback: %s %v", got, err)
	}
}

func TestInitialize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buptwc", "config.toml")
	if err := Initialize(path); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{path: 0600, filepath.Dir(path): 0700} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s: %o", p, info.Mode().Perm())
		}
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.StateDir != filepath.Join(filepath.Dir(path), "state") {
		t.Fatal(c.StateDir)
	}
	if c.Semester.StartMonday != "" || c.ValidateSchedule() == nil {
		t.Fatal("initialization must require an explicit semester date")
	}
	c.Semester.StartMonday = "2026-09-07"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	custom := []byte("# user configuration\n")
	if err := os.WriteFile(path, custom, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Initialize(path); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected already exists: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(custom) {
		t.Fatalf("changed existing file: %s %v", got, err)
	}
}

func TestInitializeDoesNotFollowExistingLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Initialize(link); !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "keep" {
		t.Fatal("target changed", err)
	}
}

func TestConcurrentInitialize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- Initialize(path) }()
	}
	success := 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if !errors.Is(err, os.ErrExist) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("successes=%d", success)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != initialConfig {
		t.Fatalf("partial config: %v", err)
	}
	if _, err := Decode(strings.NewReader(string(data))); err != nil {
		t.Fatal(err)
	}
}
