package schedule

import (
	"reflect"
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
	if len(ws) != 4 || ws[0].Start.Format("2006-01-02 15:04") != "2026-09-07 07:50" || ws[0].End.Format("2006-01-02 15:04") != "2026-09-07 08:05" || ws[1].Start.Format("15:04") != "09:32" || ws[1].End.Format("15:04") != "09:40" {
		t.Fatalf("bad course windows: %v", ws)
	}
	c.Window.Mode = "period"
	ws, err = Windows(courses, c)
	if err != nil || len(ws) != 6 || ws[0].Start.Format("2006-01-02 15:04") != "2026-09-07 07:50" || ws[0].End.Format("2006-01-02 15:04") != "2026-09-07 08:05" || ws[1].Start.Format("2006-01-02 15:04") != "2026-09-07 08:45" || ws[1].End.Format("2006-01-02 15:04") != "2026-09-07 09:00" {
		t.Fatalf("bad period windows: %v, %v", ws, err)
	}
	c.Window.Before = config.Duration(30 * time.Minute)
	c.Window.After = config.Duration(25 * time.Minute)
	ws, err = Windows(courses, c)
	if err != nil || len(ws) != 4 || ws[0].End.Format("15:04") != "09:20" {
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

func TestCourseEndWindows(t *testing.T) {
	course := func(first, last int) Course {
		return Course{Name: "网络", Weeks: []int{1}, Weekday: 1, StartPeriod: first, EndPeriod: last}
	}
	starts := []string{"12:50-13:05", "13:40-13:55", "14:35-14:50"}
	withEnd := append(append([]string(nil), starts...), "15:22-15:30")
	for _, tc := range []struct {
		name    string
		courses []Course
		modify  func(*config.Config)
		want    []string
	}{
		{"three periods", []Course{course(1, 3)}, nil, withEnd},
		{"split courses", []Course{course(3, 3), course(1, 1), course(2, 2)}, nil, withEnd},
		{"duplicate courses", []Course{course(1, 3), course(1, 3)}, nil, withEnd},
		{"course mode", []Course{course(1, 3)}, func(c *config.Config) { c.Window.Mode = "course" },
			[]string{"12:50-13:05", "15:22-15:30"}},
		{"adjacent courses in course mode", []Course{course(1, 2), course(3, 3)}, func(c *config.Config) { c.Window.Mode = "course" },
			[]string{"12:50-13:05", "14:35-14:50", "15:22-15:30"}},
		{"empty next period", []Course{course(1, 1), course(3, 3)}, nil,
			[]string{"12:50-13:05", "13:37-13:45", "14:35-14:50", "15:22-15:30"}},
		{"long break", []Course{course(4, 4), course(5, 5)}, nil,
			[]string{"15:30-15:45", "16:17-16:25", "18:20-18:35", "19:07-19:15"}},
		{"next class cancelled", []Course{course(1, 2), course(3, 3)}, func(c *config.Config) {
			c.Adjustments = []config.Adjustment{{Action: "cancel", Date: "2026-09-07", StartPeriod: 3, EndPeriod: 3}}
		}, []string{"12:50-13:05", "13:40-13:55", "14:27-14:35"}},
		{"next class moved to another day", []Course{course(1, 2), course(3, 3)}, func(c *config.Config) {
			c.Adjustments = []config.Adjustment{{Action: "move", Date: "2026-09-07", ToDate: "2026-09-08", StartPeriod: 3, EndPeriod: 3}}
		}, []string{"12:50-13:05", "13:40-13:55", "14:27-14:35", "14:35-14:50", "15:22-15:30"}},
		{"next class added", []Course{course(1, 2)}, func(c *config.Config) {
			c.Adjustments = []config.Adjustment{{Action: "add", Course: "补课", Date: "2026-09-07", StartPeriod: 3, EndPeriod: 3}}
		}, withEnd},
		{"disabled", []Course{course(1, 3)}, func(c *config.Config) { c.Window.EndBefore = 0 }, starts},
		{"custom duration", []Course{course(1, 3)}, func(c *config.Config) { c.Window.EndBefore = config.Duration(5 * time.Minute) },
			[]string{"12:50-13:05", "13:40-13:55", "14:35-14:50", "15:25-15:30"}},
		{"touching end window", []Course{course(1, 3)}, func(c *config.Config) { c.Window.After = config.Duration(37 * time.Minute) },
			[]string{"12:50-13:37", "13:40-14:27", "14:35-15:30"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testConfig(t)
			c.Window.Mode = "period"
			c.Periods = []config.Period{
				{Number: 1, Start: "13:00", End: "13:45"},
				{Number: 2, Start: "13:50", End: "14:35"},
				{Number: 3, Start: "14:45", End: "15:30"},
				{Number: 4, Start: "15:40", End: "16:25"},
				{Number: 5, Start: "18:30", End: "19:15"},
			}
			if tc.modify != nil {
				tc.modify(&c)
			}
			windows, err := Windows(tc.courses, c)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, w := range windows {
				got = append(got, w.Start.Format("15:04")+"-"+w.End.Format("15:04"))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("windows = %v, want %v", got, tc.want)
			}
		})
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
