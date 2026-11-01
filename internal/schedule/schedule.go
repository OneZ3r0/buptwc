package schedule

import (
	"fmt"
	"sort"
	"time"

	"github.com/OneZ3r0/buptwc/internal/config"
)

type Course struct {
	Name        string `json:"name"`
	Teacher     string `json:"teacher"`
	Location    string `json:"location"`
	Weeks       []int  `json:"weeks"`
	Weekday     int    `json:"weekday"` // Monday = 1, Sunday = 7.
	StartPeriod int    `json:"start_period"`
	EndPeriod   int    `json:"end_period"`
}

func Validate(courses []Course, c config.Config) error {
	if len(courses) == 0 {
		return fmt.Errorf("课表为空，未替换原课表")
	}
	for i, s := range courses {
		if s.Name == "" || s.Weekday < 1 || s.Weekday > 7 || s.StartPeriod < 1 || s.EndPeriod < s.StartPeriod || s.EndPeriod > len(c.Periods) || len(s.Weeks) == 0 {
			return fmt.Errorf("课程 %d: 名称、星期、周次或节次无效", i+1)
		}
		prev := 0
		for _, w := range s.Weeks {
			if w <= prev || w > c.Semester.Weeks {
				return fmt.Errorf("课程 %s: 周次必须递增且处于学期内", s.Name)
			}
			prev = w
		}
	}
	return nil
}

type Occurrence struct {
	Course Course
	Date   time.Time
}

type Window struct{ Start, End time.Time }

func Expand(courses []Course, c config.Config) ([]Occurrence, error) {
	if err := c.ValidateSchedule(); err != nil {
		return nil, err
	}
	if err := Validate(courses, c); err != nil {
		return nil, err
	}
	loc, _ := time.LoadLocation(c.Timezone)
	start, _ := time.ParseInLocation("2006-01-02", c.Semester.StartMonday, loc)
	var out []Occurrence
	for _, s := range courses {
		for _, week := range s.Weeks {
			out = append(out, Occurrence{s, start.AddDate(0, 0, (week-1)*7+s.Weekday-1)})
		}
	}
	for i, a := range c.Adjustments {
		date, _ := time.ParseInLocation("2006-01-02", a.Date, loc)
		if a.Action == "add" {
			out = append(out, Occurrence{Course: Course{Name: a.Course, Teacher: a.Teacher, Location: a.Location, StartPeriod: a.StartPeriod, EndPeriod: a.EndPeriod}, Date: date})
			continue
		}
		matched := false
		var next []Occurrence
		for _, o := range out {
			match := o.Date.Equal(date) && (a.Course == "" || o.Course.Name == a.Course) && (a.StartPeriod == 0 || (o.Course.StartPeriod == a.StartPeriod && o.Course.EndPeriod == a.EndPeriod))
			if match {
				matched = true
				if a.Action == "cancel" {
					continue
				}
				o.Date, _ = time.ParseInLocation("2006-01-02", a.ToDate, loc)
			}
			next = append(next, o)
		}
		if !matched {
			return nil, fmt.Errorf("adjustments[%d]: 未匹配到课程，请检查日期、名称和节次", i)
		}
		out = next
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date.Equal(out[j].Date) {
			return out[i].Course.StartPeriod < out[j].Course.StartPeriod
		}
		return out[i].Date.Before(out[j].Date)
	})
	return out, nil
}

func Windows(courses []Course, c config.Config) ([]Window, error) {
	occurrences, err := Expand(courses, c)
	if err != nil {
		return nil, err
	}
	var windows []Window
	at := func(d time.Time, hhmm string) time.Time {
		t, _ := time.Parse("15:04", hhmm)
		return time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), 0, 0, d.Location())
	}
	for _, o := range occurrences {
		first, last := o.Course.StartPeriod, o.Course.EndPeriod
		if c.Window.Mode == "course" {
			last = first
		}
		for p := first; p <= last; p++ {
			start := at(o.Date, c.Periods[p-1].Start)
			windows = append(windows, Window{start.Add(-c.Window.Before.Value()), start.Add(c.Window.After.Value())})
		}
	}
	return Merge(windows), nil
}

func Merge(windows []Window) []Window {
	out := append([]Window(nil), windows...)
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	n := 0
	for _, w := range out {
		if n > 0 && !w.Start.After(out[n-1].End) {
			if w.End.After(out[n-1].End) {
				out[n-1].End = w.End
			}
		} else {
			out[n] = w
			n++
		}
	}
	return out[:n]
}

// Next returns the active or next window. Windows are half-open [start, end).
func Next(windows []Window, now time.Time) (Window, bool) {
	i := sort.Search(len(windows), func(i int) bool { return windows[i].End.After(now) })
	if i == len(windows) {
		return Window{}, false
	}
	return windows[i], true
}
