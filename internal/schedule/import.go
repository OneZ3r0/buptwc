package schedule

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/OneZ3r0/buptwc/internal/config"
)

var weekRE = regexp.MustCompile(`(?:第)?([0-9]+(?:\s*[-,、]\s*[0-9]+)*)\s*周(?:\s*\(([单双])\))?`)
var periodRE = regexp.MustCompile(`\[[0-9]+(?:\s*-\s*[0-9]+)*\s*节\]|\[[0-9]+(?:\s*-\s*[0-9]+)*\]\s*节?|(?:第)?[0-9]+(?:\s*-\s*[0-9]+)*\s*节`)
var numberRE = regexp.MustCompile(`[0-9]+`)
var courseCodeRE = regexp.MustCompile(`^\([0-9]+\)$`)
var rowPeriodRE = regexp.MustCompile(`^([0-9]{1,2})\s*\n[0-9]{2}:[0-9]{2}-[0-9]{2}:[0-9]{2}$`)
var parityRE = regexp.MustCompile(`\(([单双])周\)`)
var separatorRE = regexp.MustCompile(`^[-=─━]{3,}$`)
var textReplacer = strings.NewReplacer("\r\n", "\n", "\r", "\n", "（", "(", "）", ")", "【", "[", "】", "]", "，", ",", "～", "-", "—", "-", "－", "-", "\u00a0", " ")
var weekReplacer = strings.NewReplacer("(周)", "周", "[周]", "周")

func normalize(s string) string {
	s = weekReplacer.Replace(textReplacer.Replace(s))
	return parityRE.ReplaceAllString(s, "周($1)")
}

func ParseWeeks(expr string, max int) ([]int, error) {
	expr = normalize(expr)
	match := weekRE.FindStringSubmatch(strings.TrimSpace(expr))
	if match == nil || strings.TrimSpace(match[0]) != strings.TrimSpace(expr) {
		return nil, fmt.Errorf("无法识别周次 %q", expr)
	}
	set := map[int]bool{}
	for _, part := range strings.Split(strings.ReplaceAll(match[1], "、", ","), ",") {
		rangeParts := strings.Split(part, "-")
		if len(rangeParts) > 2 {
			return nil, fmt.Errorf("无效周次范围")
		}
		lo, e := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
		hi := lo
		if e != nil {
			return nil, e
		}
		if len(rangeParts) == 2 {
			hi, e = strconv.Atoi(strings.TrimSpace(rangeParts[1]))
			if e != nil {
				return nil, e
			}
		}
		if lo < 1 || hi < lo || hi > max {
			return nil, fmt.Errorf("周次超出学期范围 1..%d", max)
		}
		for w := lo; w <= hi; w++ {
			if match[2] == "单" && w%2 == 0 || match[2] == "双" && w%2 == 1 {
				continue
			}
			set[w] = true
		}
	}
	out := make([]int, 0, len(set))
	for w := range set {
		out = append(out, w)
	}
	sort.Ints(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("周次为空")
	}
	return out, nil
}

// extractPeriods accepts a range or an explicitly enumerated consecutive list.
// Never reduce a non-consecutive list to its endpoints: that would monitor extra classes.
func extractPeriods(s string) (int, int, string, error) {
	matches := periodRE.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return 0, 0, s, nil
	}
	if len(matches) != 1 {
		return 0, 0, "", fmt.Errorf("重复节次")
	}
	m := matches[0]
	parts := numberRE.FindAllString(s[m[0]:m[1]], -1)
	var numbers []int
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > 24 {
			return 0, 0, "", fmt.Errorf("节次必须在 1..24 之间")
		}
		numbers = append(numbers, n)
	}
	for i := 1; i < len(numbers); i++ {
		if numbers[i] <= numbers[i-1] || len(numbers) > 2 && numbers[i] != numbers[i-1]+1 {
			return 0, 0, "", fmt.Errorf("节次必须递增且连续")
		}
	}
	return numbers[0], numbers[len(numbers)-1], strings.TrimSpace(s[:m[0]] + s[m[1]:]), nil
}

// ParseCell accepts name, optional numeric course code, teacher, weeks and location.
// Location may precede weeks; a cell must use one unambiguous layout throughout.
func ParseCell(value string, weekday, rowStart, rowEnd, maxWeeks int) ([]Course, error) {
	var lines []string
	for _, line := range strings.Split(normalize(value), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !separatorRE.MatchString(line) {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return nil, nil
	}
	var anchors []int
	for i, line := range lines {
		if weekRE.MatchString(line) {
			anchors = append(anchors, i)
		}
	}
	if len(anchors) == 0 {
		return nil, fmt.Errorf("未找到周次；预期课程名、教师、周次/节次、地点")
	}
	prefix := anchors[0]
	if len(lines) > 1 && courseCodeRE.MatchString(lines[1]) {
		prefix--
	}
	if prefix != 2 && prefix != 3 {
		return nil, fmt.Errorf("无法确定课程名、教师和地点的位置")
	}
	locationBefore := prefix == 3
	var out []Course
	start := 0
	for k, pos := range anchors {
		end := len(lines)
		if k+1 < len(anchors) {
			end = anchors[k+1] - prefix
			if end >= 0 && courseCodeRE.MatchString(lines[end]) {
				end--
			}
		}
		if start >= pos || end <= pos {
			return nil, fmt.Errorf("多课程单元格格式不明确")
		}
		name := strings.TrimPrefix(lines[start], "课程名称：")
		teacherIndex := start + 1
		if teacherIndex < pos && courseCodeRE.MatchString(lines[teacherIndex]) {
			name += lines[teacherIndex]
			teacherIndex++
		}
		expected := teacherIndex + 1
		if locationBefore {
			expected++
		}
		if expected != pos {
			return nil, fmt.Errorf("多课程单元格格式不明确，请保持课程字段顺序一致")
		}
		location := ""
		if locationBefore {
			location = lines[pos-1]
		}
		line := lines[pos]
		matches := weekRE.FindAllStringIndex(line, -1)
		if len(matches) != 1 || matches[0][0] != 0 {
			return nil, fmt.Errorf("一条排课必须以一个完整周次表达式开始")
		}
		weeks, err := ParseWeeks(line[:matches[0][1]], maxWeeks)
		if err != nil {
			return nil, err
		}
		a, b := 0, 0
		for _, tail := range append([]string{line[matches[0][1]:]}, lines[pos+1:end]...) {
			x, y, remaining, err := extractPeriods(tail)
			if err != nil {
				return nil, err
			}
			if x != 0 {
				if a != 0 {
					return nil, fmt.Errorf("重复节次")
				}
				a, b = x, y
			}
			remaining = strings.TrimSpace(remaining)
			if remaining != "" {
				if location != "" {
					return nil, fmt.Errorf("课程 %s 存在多余字段或不明确的地点", name)
				}
				location = remaining
			}
		}
		if a == 0 {
			a, b = rowStart, rowEnd
		}
		if a < 1 || b < a {
			return nil, fmt.Errorf("课程 %s 缺少有效节次", name)
		}
		out = append(out, Course{Name: name, Teacher: strings.TrimPrefix(lines[teacherIndex], "教师："), Location: strings.TrimPrefix(location, "地点："), Weeks: weeks, Weekday: weekday, StartPeriod: a, EndPeriod: b})
		start = end
	}
	return out, nil
}

func day(s string) int {
	s = strings.Join(strings.Fields(s), "")
	for i, v := range []string{"一", "二", "三", "四", "五", "六", "日"} {
		if s == "星期"+v || s == "周"+v || (i == 6 && (s == "星期天" || s == "周天")) {
			return i + 1
		}
	}
	return 0
}

// weekdayColumns recognizes candidate tables; ParseGrid rejects duplicate days.
func weekdayColumns(row []string) map[int]int {
	columns := map[int]int{}
	days := map[int]bool{}
	for col, cell := range row {
		if d := day(cell); d != 0 {
			columns[col] = d
			days[d] = true
		}
	}
	if len(days) != 7 {
		return nil
	}
	return columns
}

func ParseGrid(rows [][]string, c config.Config) ([]Course, error) {
	header := -1
	var columns map[int]int
	for r, row := range rows {
		if candidate := weekdayColumns(row); len(candidate) == 7 {
			header, columns = r, candidate
			break
		}
	}
	if header < 0 {
		return nil, fmt.Errorf("未找到星期一至星期日的课表表头")
	}
	firstDay := len(rows[header])
	for col := range columns {
		if col < firstDay {
			firstDay = col
		}
	}
	var result []Course
	seen := map[string]bool{}
	for r := header + 1; r < len(rows); r++ {
		row := rows[r]
		// Qiangzhi exports a single merged remarks cell below the timetable.
		var nonempty []string
		for _, cell := range row {
			if cell = strings.TrimSpace(cell); cell != "" {
				nonempty = append(nonempty, cell)
			}
		}
		if len(nonempty) == 1 && (strings.HasPrefix(nonempty[0], "备注：") || strings.HasPrefix(nonempty[0], "备注:")) {
			continue
		}
		a, b := 0, 0
		for col := 0; col < firstDay && col < len(row); col++ {
			label := strings.TrimSpace(normalize(row[col]))
			x, y, _, err := extractPeriods(label)
			if err != nil {
				return nil, fmt.Errorf("第 %d 行，第 %d 列: %w", r+1, col+1, err)
			}
			if m := rowPeriodRE.FindStringSubmatch(label); x == 0 && m != nil {
				x, _ = strconv.Atoi(m[1])
				y = x
			}
			if x != 0 {
				a, b = x, y
			}
		}
		for col := firstDay; col < len(row); col++ {
			d := columns[col]
			if d == 0 || strings.TrimSpace(row[col]) == "" {
				continue
			}
			courses, err := ParseCell(row[col], d, a, b, c.Semester.Weeks)
			if err == nil {
				err = Validate(courses, c)
			}
			if err != nil {
				return nil, fmt.Errorf("第 %d 行，第 %d 列: %w", r+1, col+1, err)
			}
			for _, s := range courses {
				key, _ := json.Marshal(s)
				if !seen[string(key)] {
					seen[string(key)] = true
					result = append(result, s)
				}
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Weekday != result[j].Weekday {
			return result[i].Weekday < result[j].Weekday
		}
		if result[i].StartPeriod != result[j].StartPeriod {
			return result[i].StartPeriod < result[j].StartPeriod
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].Weeks[0] < result[j].Weeks[0]
	})
	if err := Validate(result, c); err != nil {
		return nil, err
	}
	return result, nil
}
