package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"github.com/OneZ3r0/buptwc/internal/auth"
	"github.com/OneZ3r0/buptwc/internal/config"
	"github.com/OneZ3r0/buptwc/internal/monitor"
	"github.com/OneZ3r0/buptwc/internal/netutil"
	"github.com/OneZ3r0/buptwc/internal/notify"
	"github.com/OneZ3r0/buptwc/internal/schedule"
	"github.com/OneZ3r0/buptwc/internal/state"
	"github.com/OneZ3r0/buptwc/internal/store"
	"github.com/OneZ3r0/buptwc/internal/ucloud"
	"golang.org/x/term"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

func platform(c config.Config, path string) (*ucloud.Client, error) {
	s, err := auth.Load(path)
	if err != nil {
		return nil, permanentError{fmt.Errorf("无法读取会话，请先运行 buptwc login: %w", err)}
	}
	return &ucloud.Client{HTTP: netutil.Client(c.Poll.Timeout.Value()), BaseURL: auth.APIBase, Session: s, AccountID: c.UCloud.AccountID, Identity: c.UCloud.Identity, TenantID: c.UCloud.TenantID, PageSize: c.Poll.PageSize, MaxPages: c.Poll.MaxPages}, nil
}

func login(ctx context.Context, c config.Config, path string, args []string, in *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet("login", flag.ContinueOnError)
	f.SetOutput(errOut)
	username := f.String("username", "", "统一认证用户名")
	passwordStdin := f.Bool("password-stdin", false, "从标准输入读取密码")
	ticketStdin := f.Bool("ticket-stdin", false, "从标准输入读取 CAS Ticket")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *passwordStdin && *ticketStdin {
		return fmt.Errorf("无效 login 参数")
	}
	reader := bufio.NewReader(in)
	readLine := func() (string, error) {
		s, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		return strings.TrimRight(s, "\r\n"), nil
	}
	client := auth.New(c.Poll.Timeout.Value(), c.UCloud.Identity, c.UCloud.TenantID)
	var s auth.Session
	var err error
	if *ticketStdin {
		ticket, e := readLine()
		if e != nil {
			return e
		}
		s, err = client.Exchange(ctx, ticket)
	} else {
		if *username == "" {
			if *passwordStdin {
				return fmt.Errorf("--password-stdin 需要 --username")
			}
			fmt.Fprint(out, "用户名：")
			*username, err = readLine()
			if err != nil {
				return err
			}
		}
		var password string
		if *passwordStdin {
			password, err = readLine()
		} else {
			if !term.IsTerminal(int(in.Fd())) {
				return fmt.Errorf("非交互登录请使用 --username 和 --password-stdin")
			}
			fmt.Fprint(out, "密码：")
			var b []byte
			b, err = term.ReadPassword(int(in.Fd()))
			fmt.Fprintln(out)
			password = string(b)
			clear(b)
		}
		if err != nil {
			return err
		}
		s, err = client.Login(ctx, *username, password)
		password = ""
	}
	if err != nil {
		return err
	}
	if err := auth.Save(path, s); err != nil {
		return err
	}
	loc, _ := time.LoadLocation(c.Timezone)
	fmt.Fprintf(out, "登录成功，user_id=%s；会话有效至 %s。\n", s.UserID, s.ExpiresAt.In(loc).Format(time.RFC3339))
	return nil
}

func loginCommand(ctx context.Context, options commandOptions, rest []string, in *os.File, out, errOut io.Writer) error {
	c, err := options.load()
	if err != nil {
		return err
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return permanentError{err}
	}
	if c.Poll.Timeout <= 0 {
		return permanentError{fmt.Errorf("登录请求超时必须为正")}
	}
	lock, err := state.Acquire(c.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	sessionPath := filepath.Join(c.StateDir, "session.json")
	return login(ctx, c, sessionPath, rest, in, out, errOut)
}

func logoutCommand(options commandOptions, rest []string, out io.Writer) error {
	if len(rest) != 0 {
		return fmt.Errorf("logout 不接受参数")
	}
	c, err := options.load()
	if err != nil {
		return err
	}
	lock, err := state.Acquire(c.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	sessionPath := filepath.Join(c.StateDir, "session.json")

	if err := os.Remove(sessionPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Fprintln(out, "会话已清除。")
	return nil
}

func notifyCommand(ctx context.Context, options commandOptions, rest []string, out io.Writer) error {
	if len(rest) != 1 || rest[0] != "test" {
		return fmt.Errorf("用法：notify test")
	}
	c, err := options.load()
	if err != nil {
		return err
	}
	if c.Notify.Timeout <= 0 {
		return permanentError{fmt.Errorf("发送请求超时必须为正")}
	}
	lock, err := state.Acquire(c.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()

	sender, err := notify.New(os.Getenv(c.Notify.WebhookEnv), c.Notify.Timeout.Value())
	if err != nil {
		return permanentError{err}
	}
	if err := sender.Send(ctx, "[buptwc] 企业微信通知测试成功。"); err != nil {
		return err
	}
	fmt.Fprintln(out, "测试消息已发送。")
	return nil
}

func checkCommand(ctx context.Context, options commandOptions, rest []string, out io.Writer) error {
	if len(rest) != 0 {
		return fmt.Errorf("check 不接受参数")
	}
	c, err := options.load()
	if err != nil {
		return err
	}
	if err := c.ValidatePoll(); err != nil {
		return permanentError{err}
	}
	lock, err := state.Acquire(c.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	sessionPath := filepath.Join(c.StateDir, "session.json")

	client, err := platform(c, sessionPath)
	if err != nil {
		return err
	}
	ns, err := client.Scan(ctx)
	if err != nil {
		return err
	}
	matches := 0
	for _, n := range ns {
		if n.Matches() {
			matches++
		}
	}
	fmt.Fprintf(out, "完整分页成功：%d 条通知，其中 %d 条互动课堂；查询 account_id=%s（默认取 user_id，仍需核对平台映射）。\n", len(ns), matches, client.Account())
	return nil
}

func importCommand(ctx context.Context, options commandOptions, rest []string, out io.Writer) error {
	if len(rest) != 1 {
		return fmt.Errorf("用法：import <file.xls>")
	}
	c, err := options.load()
	if err != nil {
		return err
	}
	if err := c.ValidateSchedule(); err != nil {
		return permanentError{err}
	}
	lock, err := state.Acquire(c.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	db, err := store.Open(filepath.Join(c.StateDir, "buptwc.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	courses, err := schedule.Import(rest[0], c)
	if err != nil {
		return err
	}
	windows, err := schedule.Windows(courses, c)
	if err != nil {
		return permanentError{err}
	}
	if err := db.ReplaceCourses(ctx, courses, time.Now()); err != nil {
		return err
	}
	fmt.Fprintf(out, "已导入 %d 条排课，展开并合并为 %d 个监控窗口。\n", len(courses), len(windows))
	return nil

}

func runCommand(ctx context.Context, options commandOptions, rest []string, errOut io.Writer) error {
	if len(rest) != 0 {
		return fmt.Errorf("run 不接受参数")
	}
	c, err := options.load()
	if err != nil {
		return err
	}
	if err := c.ValidateSchedule(); err != nil {
		return permanentError{err}
	}
	if err := c.ValidatePoll(); err != nil {
		return permanentError{err}
	}
	if err := c.ValidateNotify(); err != nil {
		return permanentError{err}
	}
	lock, err := state.Acquire(c.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	db, err := store.Open(filepath.Join(c.StateDir, "buptwc.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	sessionPath := filepath.Join(c.StateDir, "session.json")

	courses, err := db.Courses(ctx)
	if err != nil {
		return err
	}
	windows, err := schedule.Windows(courses, c)
	if err != nil {
		return permanentError{err}
	}
	client, err := platform(c, sessionPath)
	if err != nil {
		return err
	}
	sender, err := notify.New(os.Getenv(c.Notify.WebhookEnv), c.Notify.Timeout.Value())
	if err != nil {
		return permanentError{err}
	}
	logger := slog.New(slog.NewTextHandler(errOut, nil))
	logger.Info("开始监控", "windows", len(windows), "session_expires", client.Session.ExpiresAt)
	if w, ok := schedule.Next(windows, time.Now()); ok {
		logger.Info("当前或下一监控窗口", "start", w.Start, "end", w.End)
	} else {
		logger.Warn("没有后续监控窗口，仅处理待发送任务")
	}
	r := monitor.Runner{Config: c, Store: db, Scanner: client, Sender: sender, Account: client.Account(), Windows: windows, Log: logger}
	return r.Run(ctx)
}

func coursesCommand(ctx context.Context, options commandOptions, rest []string, out io.Writer) error {
	c, err := options.load()
	if err != nil {
		return err
	}
	db, err := store.Open(filepath.Join(c.StateDir, "buptwc.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	if len(rest) != 0 {
		return fmt.Errorf("courses 不接受参数")
	}
	courses, err := db.Courses(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "课程\t教师\t地点\t星期\t节次\t周次")
	for _, s := range courses {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d-%d\t%v\n", s.Name, s.Teacher, s.Location, s.Weekday, s.StartPeriod, s.EndPeriod, s.Weeks)
	}
	return w.Flush()
}

func statusCommand(ctx context.Context, options commandOptions, rest []string, out io.Writer) error {
	c, err := options.load()
	if err != nil {
		return err
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return permanentError{err}
	}
	db, err := store.Open(filepath.Join(c.StateDir, "buptwc.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	sessionPath := filepath.Join(c.StateDir, "session.json")
	if len(rest) != 0 {
		return fmt.Errorf("status 不接受参数")
	}
	courses, courseErr := db.Courses(ctx)
	if courseErr != nil {
		fmt.Fprintln(out, "课表：", courseErr)
	} else {
		fmt.Fprintf(out, "课表：%d 条排课\n", len(courses))
	}
	s, err := auth.Load(sessionPath)
	if err != nil {
		fmt.Fprintln(out, "会话：未登录或不可读取")
		return nil
	}
	account := s.UserID
	if c.UCloud.AccountID != "" {
		account = c.UCloud.AccountID
	}
	loc, _ := time.LoadLocation(c.Timezone)
	fmt.Fprintf(out, "会话有效：%t；过期时间：%s；account_id=%s\n", s.Valid(time.Now()), s.ExpiresAt.In(loc).Format(time.RFC3339), account)
	st, err := db.Status(ctx, account)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "基线：%t；已见：%d；待发送：%d；已发送：%d；失败：%d；过期：%d\n", st.Baseline, st.Seen, st.Pending, st.Sent, st.Failed, st.Expired)
	if st.Baseline {
		fmt.Fprintln(out, "最近完整扫描：", st.LastScan.In(loc).Format(time.RFC3339))
	}
	return nil
}

func planCommand(ctx context.Context, options commandOptions, rest []string, out, errOut io.Writer) error {
	c, err := options.load()
	if err != nil {
		return err
	}
	if err := c.ValidateSchedule(); err != nil {
		return permanentError{err}
	}
	db, err := store.Open(filepath.Join(c.StateDir, "buptwc.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	loc, _ := time.LoadLocation(c.Timezone)
	f := flag.NewFlagSet("plan", flag.ContinueOnError)
	f.SetOutput(errOut)
	from := f.String("from", time.Now().In(loc).Format("2006-01-02"), "开始日期")
	days := f.Int("days", 7, "预览天数")
	if err := f.Parse(rest); err != nil {
		return err
	}
	if f.NArg() != 0 || *days < 1 || *days > 366 {
		return fmt.Errorf("plan --days 必须在 1..366 之间")
	}
	start, err := time.ParseInLocation("2006-01-02", *from, loc)
	if err != nil {
		return fmt.Errorf("--from 必须为 YYYY-MM-DD")
	}
	end := start.AddDate(0, 0, *days)
	courses, err := db.Courses(ctx)
	if err != nil {
		return err
	}
	windows, err := schedule.Windows(courses, c)
	if err != nil {
		return err
	}
	count := 0
	for _, w := range windows {
		if w.End.After(start) && w.Start.Before(end) {
			fmt.Fprintf(out, "%s → %s\n", w.Start.Format("2006-01-02 15:04:05 MST"), w.End.Format("2006-01-02 15:04:05 MST"))
			count++
		}
	}
	if count == 0 {
		fmt.Fprintln(out, "此日期范围没有监控窗口。")
	}
	return nil
}
