package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OneZ3r0/buptwc/internal/config"
	"github.com/OneZ3r0/buptwc/internal/schedule"
	"github.com/OneZ3r0/buptwc/internal/store"
)

func examplePath(t *testing.T) string {
	t.Helper()
	return "../example/Ñ§Éú¸öÈË¿Î±í_学生课表.xls"
}

// Exercise the shipped BIFF file through the public CLI in isolated state.
// No real account, CAS endpoint or enterprise WeChat webhook is used.
func TestCLIExampleImport(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "buptwc")
	build := exec.Command("go", "build", "-o", bin, "../cmd/buptwc")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	dir := t.TempDir()
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, append([]string{"--config", "testdata/config.toml", "--state-dir", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run("import", examplePath(t))
	if err != nil || !strings.Contains(out, "16 条排课") || !strings.Contains(out, "133 个监控窗口") {
		t.Fatalf("import: %v\n%s", err, out)
	}
	before, err := run("courses")
	if err != nil || !strings.Contains(before, "运动表现提升训练(112)") || !strings.Contains(before, "网络空间安全课程设计") {
		t.Fatalf("courses: %v\n%s", err, before)
	}
	out, err = run("plan", "--from", "2026-09-14", "--days", "7")
	if err != nil || strings.Count(out, "→") != 6 {
		t.Fatalf("week 2 windows: %v\n%s", err, out)
	}
	bad := filepath.Join(t.TempDir(), "broken.xls")
	if err := os.WriteFile(bad, []byte("not a timetable"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err = run("import", bad); err == nil {
		t.Fatalf("invalid import succeeded: %s", out)
	}
	after, err := run("courses")
	if err != nil || after != before {
		t.Fatalf("failed import replaced courses: %v\n%s", err, after)
	}
	if out, err = run("status"); err != nil || !strings.Contains(out, "16 条排课") {
		t.Fatalf("status: %v\n%s", err, out)
	}
}

func TestExampleCourseSemantics(t *testing.T) {
	c, err := config.Load("testdata/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	courses, err := schedule.Import(examplePath(t), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(courses) != 16 {
		t.Fatalf("got %d courses", len(courses))
	}
	counts := map[string]int{}
	for _, course := range courses {
		counts[course.Name]++
		switch course.Name {
		case "网络空间安全课程设计":
			if len(course.Weeks) != 1 || course.Weeks[0] != 2 || course.StartPeriod != 1 || course.EndPeriod != 10 || course.Weekday > 6 {
				t.Fatalf("bad full-day course: %+v", course)
			}
		case "运动表现提升训练(112)":
			if course.Teacher != "教师f" || course.Weekday != 4 || course.StartPeriod != 1 || course.EndPeriod != 2 || len(course.Weeks) != 16 {
				t.Fatalf("bad course code layout: %+v", course)
			}
		case "软件工程技术基础":
			if len(course.Weeks) != 15 || course.Weekday != 1 {
				t.Fatalf("bad discrete weeks: %+v", course)
			}
			for _, w := range course.Weeks {
				if w == 9 {
					t.Fatal("excluded week 9 imported")
				}
			}
		case "大数据安全", "软件安全", "网络安全":
			if course.StartPeriod != 6 || course.EndPeriod != 8 {
				t.Fatalf("enumerated periods lost: %+v", course)
			}
		}
	}
	for name, want := range map[string]int{"网络空间安全课程设计": 6, "网络安全": 2, "算法设计与分析": 2, "运动表现提升训练(112)": 1} {
		if counts[name] != want {
			t.Fatalf("%s: got %d, want %d", name, counts[name], want)
		}
	}
	occ, err := schedule.Expand(courses, c)
	if err != nil || len(occ) != 133 {
		t.Fatalf("occurrences: %d, %v", len(occ), err)
	}
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ReplaceCourses(context.Background(), courses, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCourses(context.Background(), nil, time.Now()); err == nil {
		t.Fatal("empty replacement accepted")
	}
	got, err := db.Courses(context.Background())
	if err != nil || len(got) != 16 {
		t.Fatalf("persisted courses: %d, %v", len(got), err)
	}
}
