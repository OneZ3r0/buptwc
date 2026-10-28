package schedule

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/OneZ3r0/buptwc/internal/config"
	"github.com/extrame/xls"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

func Import(path string, c config.Config) (courses []Course, err error) {
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
		grids, err = biffGrids(data)
		if err != nil {
			return nil, err
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

func biffGrids(data []byte) (grids [][][]string, err error) {
	// The legacy BIFF reader can panic on damaged files; never replace a good schedule.
	defer func() {
		if recover() != nil {
			grids = nil
			err = fmt.Errorf("XLS 文件损坏或格式不受支持")
		}
	}()
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
	return grids, nil
}
