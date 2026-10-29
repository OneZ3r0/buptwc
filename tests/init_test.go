package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Installation and initialization must work without a source checkout at runtime.
func TestInstalledCLIInitialization(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	install := exec.Command("go", "install", "../cmd/buptwc")
	install.Env = append(os.Environ(), "GOBIN="+binDir, "CGO_ENABLED=0")
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("go install: %v\n%s", err, out)
	}
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	// An unrelated local file must not shadow the global config.
	if err := os.WriteFile(filepath.Join(work, "config.toml"), []byte("invalid TOML"), 0600); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(dir, "config")
	path := filepath.Join(configDir, "buptwc", "config.toml")
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(binDir, "buptwc"), args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+configDir)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("status"); err == nil || !strings.Contains(out, "init") {
		t.Fatalf("missing config: %s %v", out, err)
	}
	if out, err := run("init"); err != nil || !strings.Contains(out, path) {
		t.Fatalf("init: %s %v", out, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `start_monday = ""`, `start_monday = "2026-09-07"`, 1))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := run("init"); err == nil || !strings.Contains(out, "未覆盖") {
		t.Fatalf("reinitialization: %s %v", out, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("user config overwritten", err)
	}
	fixture, err := filepath.Abs(examplePath(t))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := run("import", fixture); err != nil || !strings.Contains(out, "16 条排课") {
		t.Fatalf("import: %s %v", out, err)
	}
	if out, err := run("courses"); err != nil || !strings.Contains(out, "网络空间安全课程设计") {
		t.Fatalf("courses: %s %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "buptwc", "state", "buptwc.db")); err != nil {
		t.Fatal(err)
	}
}
