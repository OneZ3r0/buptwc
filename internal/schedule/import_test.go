package schedule

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/OneZ3r0/buptwc/internal/config"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	c, err := config.Load("../../tests/testdata/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseWeeks(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []int
	}{
		{"1-4(周)", []int{1, 2, 3, 4}},
		{"1,3,5-8周", []int{1, 3, 5, 6, 7, 8}},
		{"1-8(单周)", []int{1, 3, 5, 7}},
		{"2-8周(双)", []int{2, 4, 6, 8}},
		{"第1、3、5周", []int{1, 3, 5}},
		{"1－4（双周）", []int{2, 4}},
		{"3,1-3周", []int{1, 2, 3}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseWeeks(tc.input, 20)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	for _, input := range []string{"0-3周", "3-1周", "1-21周", "2周(单)", "1-2-3周", "1-3周备注", ""} {
		if _, err := ParseWeeks(input, 20); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestParseCellLayouts(t *testing.T) {
	want := []Course{
		{Name: "网络", Teacher: "甲老师", Location: "教一101", Weeks: []int{1, 3}, Weekday: 2, StartPeriod: 1, EndPeriod: 2},
		{Name: "数学", Teacher: "乙老师", Location: "教二202", Weeks: []int{2, 4}, Weekday: 2, StartPeriod: 3, EndPeriod: 4},
	}
	for _, tc := range []struct{ name, value string }{
		{"location after weeks", "网络\n甲老师\n1-4(单周)[01-02节]\n教一101\n数学\n乙老师\n2-4周(双)[03-04节]\n教二202"},
		{"location before weeks", "网络\n甲老师\n教一101\n1-4(单周)[01-02节]\n数学\n乙老师\n教二202\n2-4周(双)[03-04节]"},
		{"separate periods", "网络\n甲老师\n1-4(单周)\n[01-02节]\n教一101\n数学\n乙老师\n2-4周(双)\n[03-04节]\n教二202"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCell(tc.value, 2, 0, 0, 20)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v, %v; want %#v", got, err, want)
			}
		})
	}
}

const htmlTimetable = `<!doctype html><html><head><meta charset="utf-8"></head><body><table>
<tr><th colspan="2">节次</th><th>星期一</th><th>星期二</th><th>星期三</th><th>星期四</th><th>星期五</th><th>星期六</th><th>星期日</th></tr>
<tr><td rowspan="2">上午</td><td>第1-2节</td><td>网络<br>甲老师<br>1-4(单周)[01-02节]<br>教一101</td><td></td><td></td><td></td><td></td><td></td><td></td></tr>
<tr><td>第3-4节</td><td></td><td>数学<br>乙老师<br>2-4周(双)<br>教二202</td><td></td><td></td><td></td><td></td><td></td></tr>
</table></body></html>`

func TestImportHTML(t *testing.T) {
	c := testConfig(t)
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(strings.Replace(htmlTimetable, "utf-8", "gbk", 1)))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"utf8": []byte(htmlTimetable), "gbk": gbk} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "timetable.xls")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := Import(path, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || got[0].Name != "网络" || got[1].Name != "数学" || got[1].Weekday != 2 || got[1].StartPeriod != 3 {
				t.Fatalf("wrong layout: %#v", got)
			}
		})
	}
}

func TestImportRejectsInvalidFiles(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty":                  nil,
		"corrupt BIFF":           {0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1},
		"multiple timetables":    []byte(htmlTimetable + htmlTimetable),
		"duplicate weekday":      []byte(strings.Replace(htmlTimetable, "<th>星期日</th>", "<th>星期日</th><th>星期一</th>", 1)),
		"ambiguous second table": []byte(htmlTimetable + strings.Replace(htmlTimetable, "<th>星期日</th>", "<th>星期日</th><th>星期一</th>", 1)),
		"missing weekday":        []byte(strings.Replace(htmlTimetable, "<th>星期日</th>", "", 1)),
		"invalid weeks":          []byte(strings.ReplaceAll(htmlTimetable, "1-4(单周)", "0-4(单周)")),
		"invalid periods":        []byte(strings.ReplaceAll(htmlTimetable, "[01-02节]", "[00-02节]")),
		"invalid cell":           []byte(strings.ReplaceAll(htmlTimetable, "网络<br>甲老师<br>1-4(单周)[01-02节]<br>教一101", "未识别课程")),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.xls")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if got, err := Import(path, testConfig(t)); err == nil || got != nil {
				t.Fatalf("got %#v, %v", got, err)
			}
		})
	}
}

func TestExportWeekMarkers(t *testing.T) {
	for _, input := range []string{"2[周]", "2【周】", "2（周）", "2(周)"} {
		got, err := ParseWeeks(input, 20)
		if err != nil || !reflect.DeepEqual(got, []int{2}) {
			t.Fatalf("%s: %v, %v", input, got, err)
		}
	}
}

func TestExplicitPeriodLists(t *testing.T) {
	for _, tc := range []struct {
		input      string
		start, end int
	}{
		{"[01-02-03-04-05-06-07-08-09-10]节", 1, 10},
		{"[06-07-08节]", 6, 8}, {"[01-04]节", 1, 4}, {"第12节", 12, 12},
	} {
		a, b, rest, err := extractPeriods(tc.input)
		if err != nil || a != tc.start || b != tc.end || rest != "" {
			t.Fatalf("%s: %d-%d %q %v", tc.input, a, b, rest, err)
		}
	}
	for _, input := range []string{"[01-03-05]节", "[02-01]节", "[00-02]节", "[01-25]节", "[1-2节][3-4节]", "[01-02-02]节"} {
		if _, _, _, err := extractPeriods(input); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}

func TestNumericRowHeaderAndRemarks(t *testing.T) {
	grid := [][]string{
		{"节次", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六", "星期日"},
		{"3\n09:50-10:35", "网络\n甲老师\n2[周]\n教一101"},
		{"", "备注：网络 甲老师 2周;"},
	}
	got, err := ParseGrid(grid, testConfig(t))
	if err != nil || len(got) != 1 || got[0].StartPeriod != 3 || got[0].EndPeriod != 3 {
		t.Fatalf("got %v, %v", got, err)
	}
	grid[1][1] += "\n[15-16]节"
	if _, err := ParseGrid(grid, testConfig(t)); err == nil || !strings.Contains(err.Error(), "第 2 行，第 2 列") {
		t.Fatalf("missing invalid cell coordinates: %v", err)
	}
}
