package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/OneZ3r0/buptwc/internal/config"
	"github.com/OneZ3r0/buptwc/internal/schedule"
	"github.com/OneZ3r0/buptwc/internal/store"
)

const timetablePath = "../example/timetable.xls"

// Exercise a synthetic BIFF8 timetable through the public CLI in isolated state.
// No real student data, account, CAS endpoint or WeChat webhook is used.
func TestCLITimetableImport(t *testing.T) {
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
	out, err := run("import", timetablePath)
	if err != nil || !strings.Contains(out, "6 条排课") || !strings.Contains(out, "28 个监控窗口") {
		t.Fatalf("import: %v\n%s", err, out)
	}
	before, err := run("courses")
	if err != nil || !strings.Contains(before, "体能训练(999)") || !strings.Contains(before, "综合实践") {
		t.Fatalf("courses: %v\n%s", err, before)
	}
	out, err = run("plan", "--from", "2026-09-14", "--days", "7")
	if err != nil || strings.Count(out, "→") != 8 || !strings.Contains(out, "2026-09-14 17:12:00 CST → 2026-09-14 17:20:00 CST") {
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
	if out, err = run("status"); err != nil || !strings.Contains(out, "6 条排课") {
		t.Fatalf("status: %v\n%s", err, out)
	}
}

func TestTimetableCourseSemantics(t *testing.T) {
	c, err := config.Load("testdata/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	courses, err := schedule.Import(timetablePath, c)
	if err != nil {
		t.Fatal(err)
	}
	// Repeated cells must deduplicate; discrete weeks, course codes, multiple
	// courses in one cell, explicit period lists and even weeks must survive.
	want := []schedule.Course{
		{Name: "综合实践", Teacher: "测试教师甲", Location: "测试教室A", Weeks: []int{2}, Weekday: 1, StartPeriod: 1, EndPeriod: 10},
		{Name: "体能训练(999)", Teacher: "测试教师乙", Location: "测试操场", Weeks: []int{1, 2, 3, 4}, Weekday: 2, StartPeriod: 1, EndPeriod: 2},
		{Name: "程序设计", Teacher: "测试教师丙", Location: "测试教室B", Weeks: []int{1, 3, 4, 5}, Weekday: 3, StartPeriod: 3, EndPeriod: 4},
		{Name: "网络基础", Teacher: "测试教师丁", Location: "测试教室C", Weeks: []int{2, 3, 4}, Weekday: 4, StartPeriod: 6, EndPeriod: 8},
		{Name: "网络基础", Teacher: "测试教师丁", Location: "测试教室C", Weeks: []int{6}, Weekday: 4, StartPeriod: 6, EndPeriod: 8},
		{Name: "数据结构", Teacher: "测试教师戊", Location: "测试教室D", Weeks: []int{2}, Weekday: 7, StartPeriod: 11, EndPeriod: 12},
	}
	if !reflect.DeepEqual(courses, want) {
		t.Fatalf("courses: got %#v, want %#v", courses, want)
	}
	occ, err := schedule.Expand(courses, c)
	if err != nil || len(occ) != 14 {
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
	if err != nil || len(got) != len(want) {
		t.Fatalf("persisted courses: %d, %v", len(got), err)
	}
}
