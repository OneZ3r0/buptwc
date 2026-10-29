package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata"

	"github.com/OneZ3r0/buptwc/internal/auth"
	"github.com/OneZ3r0/buptwc/internal/config"
	"github.com/OneZ3r0/buptwc/internal/store"
)

const usage = `buptwc — 课表驱动的 uCloud 上课提醒

用法：buptwc [--config FILE] [--state-dir DIR] <命令>

  init                 创建用户配置文件，已有文件不覆盖
  login [--username USER] [--password-stdin | --ticket-stdin]
  logout               仅删除本地会话
  import <file.xls>    校验后原子替换课表
  courses              显示已导入排课
  plan [--from YYYY-MM-DD] [--days 7]
  check                只读检查会话与完整分页，不建立基线或发送提醒
  notify test          向配置的企业微信群发送测试消息
  status               显示会话、课表、基线与发送任务状态
  run                  按课表轮询，并恢复待发送任务

默认配置：$XDG_CONFIG_HOME/buptwc/config.toml（通常为 ~/.config/buptwc/config.toml）。
全局选项须放在命令前；首次使用请运行 buptwc init。
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(exitCode(err))
	}
}

type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

func exitCode(err error) int {
	var permanent permanentError
	if errors.As(err, &permanent) || errors.Is(err, auth.ErrSession) || errors.Is(err, store.ErrNoCourses) {
		return 2
	}
	return 1
}

func execute(ctx context.Context, args []string, in *os.File, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("buptwc", flag.ContinueOnError)
	flags.SetOutput(errOut)
	configPath := flags.String("config", "", "TOML 配置文件（默认使用用户配置目录）")
	stateDir := flags.String("state-dir", "", "覆盖状态目录")
	flags.Usage = func() { fmt.Fprint(out, usage) }
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	args = flags.Args()
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprint(out, usage)
		return nil
	}
	options := commandOptions{configPath: *configPath, stateDir: *stateDir}
	rest := args[1:]
	switch args[0] {
	case "init":
		return initCommand(options, rest, out)
	case "login":
		return loginCommand(ctx, options, rest, in, out, errOut)
	case "logout":
		return logoutCommand(options, rest, out)
	case "notify":
		return notifyCommand(ctx, options, rest, out)
	case "check":
		return checkCommand(ctx, options, rest, out)
	case "import":
		return importCommand(ctx, options, rest, out)
	case "run":
		return runCommand(ctx, options, rest, errOut)
	case "courses":
		return coursesCommand(ctx, options, rest, out)
	case "status":
		return statusCommand(ctx, options, rest, out)
	case "plan":
		return planCommand(ctx, options, rest, out, errOut)
	default:
		return fmt.Errorf("未知命令 %q", args[0])
	}
}

type commandOptions struct{ configPath, stateDir string }

func (o commandOptions) path() (string, error) {
	if o.configPath != "" {
		return o.configPath, nil
	}
	return config.DefaultPath()
}

func (o commandOptions) load() (config.Config, error) {
	path, err := o.path()
	if err != nil {
		return config.Config{}, permanentError{err}
	}
	c, err := config.Load(path)
	if os.IsNotExist(err) {
		return c, permanentError{fmt.Errorf("配置不存在：%s；请运行 buptwc --config %q init: %w", path, path, err)}
	}
	if err != nil {
		return c, permanentError{err}
	}
	if o.stateDir != "" {
		c.StateDir = o.stateDir
	}
	if c.StateDir == "" {
		return c, permanentError{fmt.Errorf("state_dir 不能为空")}
	}
	return c, nil
}
