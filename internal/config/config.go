package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Duration time.Duration

func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	*d = Duration(v)
	return err
}

func (d Duration) Value() time.Duration { return time.Duration(d) }

type Period struct {
	Number int    `toml:"number"`
	Start  string `toml:"start"`
	End    string `toml:"end"`
}

type Adjustment struct {
	Action      string `toml:"action"`
	Course      string `toml:"course"`
	Date        string `toml:"date"`
	ToDate      string `toml:"to_date"`
	StartPeriod int    `toml:"start_period"`
	EndPeriod   int    `toml:"end_period"`
	Teacher     string `toml:"teacher"`
	Location    string `toml:"location"`
}

type Config struct {
	Timezone string `toml:"timezone"`
	StateDir string `toml:"state_dir"`
	Semester struct {
		StartMonday string `toml:"start_monday"`
		Weeks       int    `toml:"weeks"`
	} `toml:"semester"`
	Periods     []Period     `toml:"periods"`
	Adjustments []Adjustment `toml:"adjustments"`
	Window      struct {
		Mode   string   `toml:"mode"`
		Before Duration `toml:"before"`
		After  Duration `toml:"after"`
	} `toml:"window"`
	Poll struct {
		Interval   Duration `toml:"interval"`
		PageSize   int      `toml:"page_size"`
		MaxPages   int      `toml:"max_pages"`
		Timeout    Duration `toml:"timeout"`
		BackoffMax Duration `toml:"backoff_max"`
	} `toml:"poll"`
	UCloud struct {
		AccountID string `toml:"account_id"`
		Identity  string `toml:"identity"`
		TenantID  string `toml:"tenant_id"`
	} `toml:"ucloud"`
	Notify struct {
		WebhookEnv  string   `toml:"webhook_env"`
		TTL         Duration `toml:"ttl"`
		MaxAttempts int      `toml:"max_attempts"`
		RetryBase   Duration `toml:"retry_base"`
		RetryMax    Duration `toml:"retry_max"`
		Timeout     Duration `toml:"timeout"`
	} `toml:"notify"`
}

func Defaults() Config {
	var c Config
	c.Timezone = "Asia/Shanghai"
	c.StateDir = "state"
	c.Semester.Weeks = 20
	c.Window.Mode = "course"
	c.Window.Before = Duration(10 * time.Minute)
	c.Window.After = Duration(5 * time.Minute)
	c.Poll.Interval = Duration(10 * time.Second)
	c.Poll.PageSize = 10
	c.Poll.MaxPages = 100
	c.Poll.Timeout = Duration(15 * time.Second)
	c.Poll.BackoffMax = Duration(5 * time.Minute)
	c.UCloud.Identity = "JS005:undefined"
	c.UCloud.TenantID = "000000"
	c.Notify.WebhookEnv = "BUPTWC_WEBHOOK"
	c.Notify.TTL = Duration(30 * time.Minute)
	c.Notify.MaxAttempts = 6
	c.Notify.RetryBase = Duration(5 * time.Second)
	c.Notify.RetryMax = Duration(5 * time.Minute)
	c.Notify.Timeout = Duration(10 * time.Second)
	return c
}

func Decode(r io.Reader) (Config, error) {
	c := Defaults()
	if err := toml.NewDecoder(r).DisallowUnknownFields().Decode(&c); err != nil {
		return c, fmt.Errorf("配置格式错误: %w", err)
	}
	return c, c.Validate()
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	c, err := Decode(f)
	if err == nil && !filepath.IsAbs(c.StateDir) {
		c.StateDir = filepath.Join(filepath.Dir(path), c.StateDir)
	}
	return c, err
}

func (c Config) Validate() error {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return fmt.Errorf("无效时区: %w", err)
	}
	start, err := time.ParseInLocation("2006-01-02", c.Semester.StartMonday, loc)
	if err != nil || start.Weekday() != time.Monday {
		return fmt.Errorf("semester.start_monday 必须是第一教学周周一 (YYYY-MM-DD)")
	}
	if c.Semester.Weeks < 1 || c.Semester.Weeks > 60 {
		return fmt.Errorf("semester.weeks 必须在 1..60 之间")
	}
	if c.StateDir == "" {
		return fmt.Errorf("state_dir 不能为空")
	}
	if len(c.Periods) == 0 || len(c.Periods) > 24 {
		return fmt.Errorf("请配置 1..24 个节次")
	}
	last := "00:00"
	for i, p := range c.Periods {
		_, e1 := time.Parse("15:04", p.Start)
		_, e2 := time.Parse("15:04", p.End)
		if p.Number != i+1 || e1 != nil || e2 != nil || len(p.Start) != 5 || len(p.End) != 5 || p.Start < last || p.Start >= p.End {
			return fmt.Errorf("periods[%d]: 节次须从 1 连续编号，时间须递增且不重叠 (HH:MM)", i)
		}
		last = p.End
	}
	if c.Window.Mode != "course" && c.Window.Mode != "period" {
		return fmt.Errorf("window.mode 必须为 course 或 period")
	}
	if c.Window.Before < 0 || c.Window.After < 0 || c.Window.Before.Value() > 24*time.Hour || c.Window.After.Value() > 24*time.Hour {
		return fmt.Errorf("窗口偏移必须在 0..24h 之间")
	}
	if c.Poll.Interval.Value() < time.Second || c.Poll.Timeout <= 0 || c.Poll.BackoffMax < c.Poll.Interval || c.Poll.PageSize < 1 || c.Poll.PageSize > 1000 || c.Poll.MaxPages < 1 || c.Poll.MaxPages > 10000 {
		return fmt.Errorf("无效的 poll 间隔、超时、退避或分页配置")
	}
	if c.Notify.TTL <= 0 || c.Notify.Timeout <= 0 || c.Notify.MaxAttempts < 1 || c.Notify.MaxAttempts > 100 || c.Notify.RetryBase <= 0 || c.Notify.RetryMax < c.Notify.RetryBase || c.Notify.WebhookEnv == "" {
		return fmt.Errorf("无效的 notify 有效期、重试或超时配置")
	}
	for i, a := range c.Adjustments {
		if _, err := time.ParseInLocation("2006-01-02", a.Date, loc); err != nil {
			return fmt.Errorf("adjustments[%d]: 无效 date", i)
		}
		if a.Action != "cancel" && a.Action != "move" && a.Action != "add" {
			return fmt.Errorf("adjustments[%d]: action 必须为 cancel/move/add", i)
		}
		if a.Action == "move" {
			if _, err := time.ParseInLocation("2006-01-02", a.ToDate, loc); err != nil {
				return fmt.Errorf("adjustments[%d]: 无效 to_date", i)
			}
		}
		if a.Action == "add" && (a.Course == "" || a.StartPeriod == 0) {
			return fmt.Errorf("adjustments[%d]: 补课需要 course 和节次", i)
		}
		if a.StartPeriod != 0 || a.EndPeriod != 0 {
			if a.StartPeriod < 1 || a.EndPeriod < a.StartPeriod || a.EndPeriod > len(c.Periods) {
				return fmt.Errorf("adjustments[%d]: 无效节次", i)
			}
		}
	}
	return nil
}
