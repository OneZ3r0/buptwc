package config

import (
	"strings"
	"testing"
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
