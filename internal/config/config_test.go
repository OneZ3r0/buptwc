package config

import (
	"strings"
	"testing"
	"time"
)

func TestIndependentValidation(t *testing.T) {
	c, err := Decode(strings.NewReader("[semester]\nstart_monday='invalid'\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.ValidateSchedule() == nil {
		t.Fatal("invalid semester accepted")
	}
	if err := c.ValidatePoll(); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateNotify(); err != nil {
		t.Fatal(err)
	}
	c, err = Load("../../tests/testdata/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Notify.MaxAttempts = 0
	if err := c.ValidateSchedule(); err != nil {
		t.Fatal("schedule depends on notifications:", err)
	}
	if c.ValidateNotify() == nil {
		t.Fatal("invalid notify accepted")
	}
	if _, err := Decode(strings.NewReader("unknown=1")); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestEndWindowConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  time.Duration
		valid bool
	}{
		{"existing config", "[window]\nbefore='10m'\nafter='5m'", 8 * time.Minute, true},
		{"custom", "[window]\nend_before='3m'", 3 * time.Minute, true},
		{"disabled", "[window]\nend_before='0m'", 0, true},
		{"negative", "[window]\nend_before='-1m'", -time.Minute, false},
		{"too large", "[window]\nend_before='24h1s'", 24*time.Hour + time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Decode(strings.NewReader(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			if c.Window.EndBefore.Value() != tc.want {
				t.Fatalf("end_before = %v, want %v", c.Window.EndBefore.Value(), tc.want)
			}
			c.Semester.StartMonday = "2026-09-07"
			c.Periods = []Period{{Number: 1, Start: "13:00", End: "13:45"}}
			if err := c.ValidateSchedule(); (err == nil) != tc.valid {
				t.Fatalf("ValidateSchedule() = %v, valid = %t", err, tc.valid)
			}
		})
	}
}
