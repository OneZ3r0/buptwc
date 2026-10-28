package monitor

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/OneZ3r0/buptwc/internal/auth"
	"github.com/OneZ3r0/buptwc/internal/config"
	"github.com/OneZ3r0/buptwc/internal/netutil"
	"github.com/OneZ3r0/buptwc/internal/notify"
	"github.com/OneZ3r0/buptwc/internal/schedule"
	"github.com/OneZ3r0/buptwc/internal/store"
	"github.com/OneZ3r0/buptwc/internal/ucloud"
)

type Scanner interface {
	Scan(context.Context) ([]ucloud.Notification, error)
}
type Sender interface {
	Send(context.Context, string) error
}

type Runner struct {
	Config  config.Config
	Store   *store.Store
	Scanner Scanner
	Sender  Sender
	Account string
	Windows []schedule.Window
	Log     *slog.Logger
}

func (r *Runner) Poll(ctx context.Context, now time.Time) (store.ScanResult, error) {
	notifications, err := r.Scanner.Scan(ctx)
	if err != nil {
		return store.ScanResult{}, err
	}
	loc, err := time.LoadLocation(r.Config.Timezone)
	if err != nil {
		return store.ScanResult{}, err
	}
	records := make([]store.Observed, 0, len(notifications))
	for _, n := range notifications {
		record := store.Observed{ID: string(n.ID)}
		if n.Matches() {
			at := n.Timestamp(loc, now)
			record.ExpiresAt = at.Add(r.Config.Notify.TTL.Value())
			record.Message = notify.Message(n, at.In(loc))
		}
		records = append(records, record)
	}
	return r.Store.ApplyScan(ctx, r.Account, records, now)
}

// Dispatch is independent of course windows and authentication availability.
func (r *Runner) Dispatch(ctx context.Context, now time.Time) error {
	jobs, err := r.Store.Due(ctx, r.Account, now)
	if err != nil {
		return err
	}
	// Bound one batch so notification retries do not starve platform polling.
	if len(jobs) > 10 {
		jobs = jobs[:10]
	}
	for _, j := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !j.ExpiresAt.After(time.Now()) {
			if err := r.Store.Finish(ctx, j.ID, "expired", j.Attempts, time.Time{}, ""); err != nil {
				return err
			}
			continue
		}
		attempt := j.Attempts + 1
		if attempt > r.Config.Notify.MaxAttempts {
			if err := r.Store.Finish(ctx, j.ID, "failed", j.Attempts, time.Time{}, "达到重试上限"); err != nil {
				return err
			}
			continue
		}
		sendCtx, cancel := context.WithDeadline(ctx, j.ExpiresAt)
		err := r.Sender.Send(sendCtx, j.Message)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		state, lastError, next := "sent", "", time.Time{}
		if err != nil {
			lastError = err.Error()
			next = time.Now().Add(netutil.Backoff(r.Config.Notify.RetryBase.Value(), r.Config.Notify.RetryMax.Value(), attempt))
			state = "pending"
			if attempt >= r.Config.Notify.MaxAttempts {
				state = "failed"
			} else if !next.Before(j.ExpiresAt) {
				state = "expired"
			}
			r.logger().Warn("提醒发送失败", "job", j.ID, "attempt", attempt, "state", state, "error", err)
		} else {
			r.logger().Info("提醒已发送", "job", j.ID)
		}
		if err := r.Store.Finish(ctx, j.ID, state, attempt, next, lastError); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func (r *Runner) Run(ctx context.Context) error {
	var nextPoll time.Time
	failures := 0
	authFailed := false
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := r.Dispatch(ctx, time.Now()); err != nil {
			return err
		}
		now := time.Now()
		w, hasWindow := schedule.Next(r.Windows, now)
		active := hasWindow && !now.Before(w.Start)
		if !authFailed && active && !now.Before(nextPoll) {
			result, err := r.Poll(ctx, now)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, auth.ErrSession) {
				authFailed = true
				r.logger().Error("会话失效；停止轮询，继续处理待发送任务", "error", err)
			} else if err != nil {
				failures++
				nextPoll = time.Now().Add(netutil.Backoff(r.Config.Poll.Interval.Value(), r.Config.Poll.BackoffMax.Value(), failures))
				r.logger().Warn("完整扫描失败，稍后重试", "error", err, "retry_at", nextPoll)
			} else {
				failures = 0
				nextPoll = time.Now().Add(r.Config.Poll.Interval.Value())
				if result.Baseline {
					r.logger().Info("已建立通知基线，历史通知不推送", "records", result.New)
				} else if result.Queued > 0 {
					r.logger().Info("新增待发送提醒", "count", result.Queued)
				}
			}
			continue // Deliver newly committed jobs immediately.
		}
		wake, hasJob, err := r.Store.NextJob(ctx, r.Account)
		if err != nil {
			return err
		}
		if authFailed && !hasJob {
			return auth.ErrSession
		}
		if !authFailed && hasWindow {
			pollAt := w.Start
			if active {
				pollAt = nextPoll
				if pollAt.After(w.End) {
					pollAt = w.End
				}
			}
			if !hasJob || pollAt.Before(wake) {
				wake = pollAt
			}
		}
		if wake.IsZero() {
			<-ctx.Done()
			return ctx.Err()
		}
		delay := time.Until(wake)
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
