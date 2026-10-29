package main

import (
	"context"
	"errors"
	"github.com/OneZ3r0/buptwc/internal/auth"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogoutIgnoresSchedule(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("[semester]\nstart_monday='invalid'\n"), 0600)
	session := filepath.Join(dir, "session.json")
	os.WriteFile(session, []byte("test"), 0600)
	if e := execute(context.Background(), []string{"--config", cfg, "--state-dir", dir, "logout"}, os.Stdin, io.Discard, io.Discard); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(session); !os.IsNotExist(e) {
		t.Fatal("session not removed")
	}
}
func TestExitCodes(t *testing.T) {
	for _, c := range []struct {
		e    error
		want int
	}{{errors.New("database busy"), 1}, {permanentError{errors.New("configuration")}, 2}, {auth.ErrSession, 2}} {
		if exitCode(c.e) != c.want {
			t.Fatal(c)
		}
	}
}

func TestCommandsValidateOnlyTheirInputs(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	data := "[semester]\nstart_monday='invalid'\n[poll]\npage_size=0\n[notify]\nmax_attempts=0\nttl='-1s'\n"
	if err := os.WriteFile(cfg, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	input, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	t.Setenv("BUPTWC_WEBHOOK", "")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"login", "--ticket-stdin"}, "Ticket 不能为空"},
		{[]string{"notify", "test"}, "Webhook"},
		{[]string{"check"}, "分页配置"},
	} {
		args := append([]string{"--config", cfg, "--state-dir", dir}, tc.args...)
		err := execute(context.Background(), args, input, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: got %v want %s", tc.args, err, tc.want)
		}
	}
}

func TestUnknownCommandDoesNotRequireConfig(t *testing.T) {
	err := execute(context.Background(), []string{"--config", "/missing/config.toml", "unknown"}, os.Stdin, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "未知命令") {
		t.Fatal(err)
	}
}

func TestInvalidCommandArgumentsDoNotCreateState(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"logout", "extra"}, {"notify"}, {"check", "extra"}, {"import"}, {"run", "extra"}} {
		statePath := filepath.Join(dir, args[0])
		err := execute(context.Background(), append([]string{"--config", cfg, "--state-dir", statePath}, args...), os.Stdin, io.Discard, io.Discard)
		if err == nil {
			t.Fatalf("accepted %v", args)
		}
		if _, err := os.Stat(statePath); !os.IsNotExist(err) {
			t.Fatalf("created state for %v: %v", args, err)
		}
	}
}

func TestExplicitInitPathOverridesDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "invalid-relative-directory")
	path := filepath.Join(t.TempDir(), "custom", "config.toml")
	if err := execute(context.Background(), []string{"--config", path, "init"}, os.Stdin, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestInitRejectsUnusedArguments(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	for _, args := range [][]string{{"init", "extra"}, {"--state-dir", dir, "init"}} {
		if err := execute(context.Background(), args, os.Stdin, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unexpected files: %v %v", entries, err)
	}
}
