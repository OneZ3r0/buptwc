package schedule

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/OneZ3r0/buptwc/internal/config"
	"github.com/extrame/xls"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

var weekRE = regexp.MustCompile(`(?:第)?([0-9]+(?:\s*[-,、]\s*[0-9]+)*)\s*周(?:\s*\(([单双])\))?`)
var periodRE = regexp.MustCompile(`\[?([0-9]{1,2})(?:\s*-\s*([0-9]{1,2}))?\s*节\]?`)
var bracketPeriodRE = regexp.MustCompile(`\[([0-9]{1,2})(?:-([0-9]{1,2}))?\]`)
var parityRE = regexp.MustCompile(`\(([单双])周\)`)
var separatorRE = regexp.MustCompile(`^[-=─━]{3,}$`)

func normalize(s string) string {
	r := strings.NewReplacer("\r\n", "\n", "\r", "\n", "（", "(", "）", ")", "【", "[", "】", "]", "，", ",", "～", "-", "—", "-", "－", "-", "(周)", "周", "\u00a0", " ")
	s = r.Replace(s)
	s = parityRE.ReplaceAllString(s, "周($1)")
	return s
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

func periods(s string) (int, int) {
	m := periodRE.FindStringSubmatch(s)
	if m == nil {
		m = bracketPeriodRE.FindStringSubmatch(s)
	}
	if m == nil {
		return 0, 0
	}
	a, _ := strconv.Atoi(m[1])
	b := a
	if m[2] != "" {
		b, _ = strconv.Atoi(m[2])
	}
	return a, b
}

// ParseCell accepts Qiangzhi blocks: name, teacher, weeks/periods, location.
// A location may also precede the weeks line. Ambiguous blocks fail the import.
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
	var out []Course
	start := 0
	for k, pos := range anchors {
		end := len(lines)
		if k+1 < len(anchors) {
			end = anchors[k+1] - 2
		}
		if pos-start < 2 || pos-start > 3 || end <= pos {
			return nil, fmt.Errorf("多课程单元格格式不明确，请使用每门课四行：课程名、教师、周次/节次、地点")
		}
		line := lines[pos]
		matches := weekRE.FindAllString(line, -1)
		if len(matches) != 1 {
			return nil, fmt.Errorf("一条排课必须只有一个周次表达式")
		}
		weeks, err := ParseWeeks(matches[0], maxWeeks)
		if err != nil {
			return nil, err
		}
		a, b := periods(line)
		location := ""
		if pos-start == 3 {
			location = lines[pos-1]
		}
		residual := strings.TrimSpace(bracketPeriodRE.ReplaceAllString(periodRE.ReplaceAllString(weekRE.ReplaceAllString(line, ""), ""), ""))
		residual = strings.Trim(residual, " [](){}:：;")
		if residual != "" {
			location = strings.TrimSpace(location + " " + residual)
		}
		for _, tail := range lines[pos+1 : end] {
			if x, y := periods(tail); x != 0 {
				if a != 0 {
					return nil, fmt.Errorf("重复节次")
				}
				a, b = x, y
				tail = strings.TrimSpace(bracketPeriodRE.ReplaceAllString(periodRE.ReplaceAllString(tail, ""), ""))
			}
			if tail != "" {
				location = strings.TrimSpace(location + " " + tail)
			}
		}
		if a == 0 {
			a, b = rowStart, rowEnd
		}
		if a == 0 {
			return nil, fmt.Errorf("课程 %s 缺少节次", lines[start])
		}
		out = append(out, Course{Name: strings.TrimPrefix(lines[start], "课程名称："), Teacher: strings.TrimPrefix(lines[start+1], "教师："), Location: strings.TrimPrefix(location, "地点："), Weeks: weeks, Weekday: weekday, StartPeriod: a, EndPeriod: b})
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

func ParseGrid(rows [][]string, c config.Config) ([]Course, error) {
	columns := map[int]int{}
	header := -1
	for r, row := range rows {
		candidate := map[int]int{}
		days := map[int]bool{}
		for col, cell := range row {
			if d := day(cell); d != 0 {
				candidate[col] = d
				days[d] = true
			}
		}
		if len(days) == 7 && len(candidate) == 7 {
			columns = candidate
			header = r
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
		a, b := 0, 0
		for col := 0; col < firstDay && col < len(row); col++ {
			if x, y := periods(normalize(row[col])); x != 0 {
				a, b = x, y
			}
		}
		for col, d := range columns {
			if col >= len(row) || strings.TrimSpace(row[col]) == "" {
				continue
			}
			if day(row[col]) != 0 {
				continue
			}
			courses, err := ParseCell(row[col], d, a, b, c.Semester.Weeks)
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
		return result[i].StartPeriod < result[j].StartPeriod
	})
	return result, Validate(result, c)
}

func Import(path string, c config.Config) (courses []Course, err error) {
	// The legacy BIFF reader can panic on damaged files; never replace a good schedule.
	defer func() {
		if recover() != nil {
			courses = nil
			err = fmt.Errorf("XLS 文件损坏或格式不受支持")
		}
	}()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("课表超过 16 MiB")
	}
	var grids [][][]string
	if bytes.HasPrefix(data, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}) {
		book, e := xls.OpenReader(bytes.NewReader(data), "utf-8")
		if e != nil {
			return nil, e
		}
		if book == nil {
			return nil, fmt.Errorf("XLS 中没有 Workbook")
		}
		for s := 0; s < book.NumSheets(); s++ {
			sheet := book.GetSheet(s)
			if sheet == nil {
				continue
			}
			if sheet.MaxRow > 5000 {
				return nil, fmt.Errorf("课表行数超过 5000")
			}
			var rows [][]string
			for r := 0; r <= int(sheet.MaxRow); r++ {
				row := xlsRow(sheet, r)
				var cells []string
				if row != nil {
					if row.LastCol() > 256 {
						return nil, fmt.Errorf("课表列数超过 256")
					}
					for col := 0; col < row.LastCol(); col++ {
						cells = append(cells, row.Col(col))
					}
				}
				rows = append(rows, cells)
			}
			grids = append(grids, rows)
		}
	} else {
		grids, err = htmlGrids(data)
		if err != nil {
			return nil, err
		}
	}
	var match [][]string
	for _, grid := range grids {
		found := false
		for _, row := range grid {
			days := map[int]bool{}
			for _, cell := range row {
				if d := day(cell); d != 0 {
					days[d] = true
				}
			}
			if len(days) == 7 {
				found = true
				break
			}
		}
		if found {
			if match != nil {
				return nil, fmt.Errorf("存在多个课表，请导出单个学期课表")
			}
			match = grid
		}
	}
	if match == nil {
		return nil, fmt.Errorf("未识别强智课表；支持 BIFF XLS 或含星期表头的 HTML XLS")
	}
	return ParseGrid(match, c)
}

func xlsRow(sheet *xls.WorkSheet, i int) (row *xls.Row) {
	defer func() { _ = recover() }()
	return sheet.Row(i)
}

func htmlGrids(data []byte) ([][][]string, error) {
	r, err := charset.NewReader(bytes.NewReader(data), "")
	if err != nil {
		return nil, err
	}
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var tables []*html.Node
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "table" {
			tables = append(tables, n)
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			visit(ch)
		}
	}
	visit(doc)
	var grids [][][]string
	for _, table := range tables {
		var trs []*html.Node
		var collect func(*html.Node)
		collect = func(n *html.Node) {
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				if ch.Type == html.ElementNode && ch.Data == "table" {
					continue
				}
				if ch.Type == html.ElementNode && ch.Data == "tr" {
					trs = append(trs, ch)
				} else {
					collect(ch)
				}
			}
		}
		collect(table)
		if len(trs) > 5000 {
			return nil, fmt.Errorf("课表行数超过 5000")
		}
		occupied := map[[2]int]bool{}
		var rows [][]string
		for ri, tr := range trs {
			row := make([]string, 256)
			col := 0
			for cell := tr.FirstChild; cell != nil; cell = cell.NextSibling {
				if cell.Type != html.ElementNode || (cell.Data != "td" && cell.Data != "th") {
					continue
				}
				for occupied[[2]int{ri, col}] {
					col++
				}
				if col >= 256 {
					return nil, fmt.Errorf("课表列数超过 256")
				}
				rs, cs := 1, 1
				for _, a := range cell.Attr {
					if a.Key == "rowspan" {
						rs, _ = strconv.Atoi(a.Val)
					}
					if a.Key == "colspan" {
						cs, _ = strconv.Atoi(a.Val)
					}
				}
				if rs < 1 || cs < 1 || rs > 5000 || col+cs > 256 {
					return nil, fmt.Errorf("无效合并单元格")
				}
				row[col] = cellText(cell)
				for y := 0; y < rs; y++ {
					for x := 0; x < cs; x++ {
						occupied[[2]int{ri + y, col + x}] = true
					}
				}
				col += cs
			}
			rows = append(rows, row)
		}
		grids = append(grids, rows)
	}
	return grids, nil
}

func cellText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && (n.Data == "br" || n.Data == "p" || n.Data == "div") {
			b.WriteByte('\n')
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
		if n.Type == html.ElementNode && (n.Data == "p" || n.Data == "div") {
			b.WriteByte('\n')
		}
	}
	walk(n)
	return b.String()
}
