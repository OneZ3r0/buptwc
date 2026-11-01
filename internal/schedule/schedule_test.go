package schedule

import (
	"testing"
	"time"

	"github.com/OneZ3r0/buptwc/internal/config"
)

func TestWindowsAndAdjustments(t *testing.T) {
	c := testConfig(t)
	c.Semester.StartMonday = "2026-09-07"
	c.Window.Before = config.Duration(10 * time.Minute)
	c.Window.After = config.Duration(5 * time.Minute)
	c.Periods = []config.Period{{Number: 1, Start: "08:00", End: "08:45"}, {Number: 2, Start: "08:55", End: "09:40"}}
	courses := []Course{{Name: "网络", Weeks: []int{1, 3}, Weekday: 1, StartPeriod: 1, EndPeriod: 2}}
	c.Window.Mode = "course"
	ws, err := Windows(courses, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 || ws[0].Start.Format("2006-01-02 15:04") != "2026-09-07 07:50" || ws[0].End.Format("2006-01-02 15:04") != "2026-09-07 08:05" {
		t.Fatalf("bad course windows: %v", ws)
	}
	c.Window.Mode = "period"
	ws, err = Windows(courses, c)
	if err != nil || len(ws) != 4 || ws[0].Start.Format("2006-01-02 15:04") != "2026-09-07 07:50" || ws[0].End.Format("2006-01-02 15:04") != "2026-09-07 08:05" || ws[1].Start.Format("2006-01-02 15:04") != "2026-09-07 08:45" || ws[1].End.Format("2006-01-02 15:04") != "2026-09-07 09:00" {
		t.Fatalf("bad period windows: %v, %v", ws, err)
	}
	c.Window.Before = config.Duration(30 * time.Minute)
	c.Window.After = config.Duration(25 * time.Minute)
	ws, err = Windows(courses, c)
	if err != nil || len(ws) != 2 {
		t.Fatalf("touching windows not merged: %v, %v", ws, err)
	}
	c.Adjustments = []config.Adjustment{
		{Action: "move", Course: "网络", Date: "2026-09-07", ToDate: "2026-09-08"},
		{Action: "cancel", Course: "网络", Date: "2026-09-21"},
		{Action: "add", Course: "补课", Date: "2026-09-09", StartPeriod: 1, EndPeriod: 1},
	}
	occ, err := Expand(courses, c)
	if err != nil || len(occ) != 2 || occ[0].Date.Format("2006-01-02") != "2026-09-08" || occ[1].Course.Name != "补课" {
		t.Fatalf("bad adjustments: %v, %v", occ, err)
	}
	c.Adjustments = append(c.Adjustments, config.Adjustment{Action: "cancel", Date: "2026-09-10"})
	if _, err := Expand(courses, c); err == nil {
		t.Fatal("unmatched adjustment accepted")
	}
}

func TestNextHalfOpen(t *testing.T) {
	at := time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC)
	windows := []Window{{at, at.Add(time.Hour)}, {at.Add(2 * time.Hour), at.Add(3 * time.Hour)}}
	for _, tc := range []struct {
		offset time.Duration
		index  int
	}{
		{-time.Second, 0}, {0, 0}, {time.Hour, 1}, {2 * time.Hour, 1}, {3 * time.Hour, -1},
	} {
		w, ok := Next(windows, at.Add(tc.offset))
		if tc.index < 0 {
			if ok {
				t.Fatal("window returned at final end")
			}
			continue
		}
		if !ok || w != windows[tc.index] {
			t.Fatalf("offset %v: %v, %t", tc.offset, w, ok)
		}
	}
}
