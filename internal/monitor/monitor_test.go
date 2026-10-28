package monitor

import (
	"context"
	"errors"
	"github.com/OneZ3r0/buptwc/internal/auth"
	"github.com/OneZ3r0/buptwc/internal/config"
	"github.com/OneZ3r0/buptwc/internal/schedule"
	"github.com/OneZ3r0/buptwc/internal/store"
	"github.com/OneZ3r0/buptwc/internal/ucloud"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type scanFunc func(context.Context) ([]ucloud.Notification, error)

func (f scanFunc) Scan(c context.Context) ([]ucloud.Notification, error) { return f(c) }

type sendFunc func(context.Context, string) error

func (f sendFunc) Send(c context.Context, s string) error { return f(c, s) }
func TestRunWindowBackoffAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, e := store.Open(filepath.Join(t.TempDir(), "state.db"))
		if e != nil {
			t.Fatal(e)
		}
		defer db.Close()
		c := config.Defaults()
		c.Poll.Interval = config.Duration(time.Second)
		now := time.Now()
		var calls atomic.Int32
		r := Runner{Config: c, Store: db, Account: "A", Windows: []schedule.Window{{Start: now.Add(time.Minute), End: now.Add(65 * time.Second)}}, Scanner: scanFunc(func(context.Context) ([]ucloud.Notification, error) {
			calls.Add(1)
			if calls.Load() < 3 {
				return nil, errors.New("temporary")
			}
			return nil, nil
		})}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- r.Run(ctx) }()
		synctest.Wait()
		if calls.Load() != 0 {
			t.Fatal("polled outside window")
		}
		time.Sleep(60 * time.Second)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("calls=%d", calls.Load())
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("calls=%d", calls.Load())
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("backoff ignored")
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		n := calls.Load()
		time.Sleep(time.Minute)
		synctest.Wait()
		if calls.Load() != n {
			t.Fatal("polled after window")
		}
		cancel()
		if !errors.Is(<-done, context.Canceled) {
			t.Fatal("cancel failed")
		}
	})
}
func TestRunDrainsAfterSessionFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := testStore(t)
		ctx := context.Background()
		now := time.Now()
		if _, err := db.ApplyScan(ctx, "A", nil, now); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ApplyScan(ctx, "A", []store.Observed{{ID: "1", Message: "test", ExpiresAt: now.Add(time.Hour)}}, now); err != nil {
			t.Fatal(err)
		}
		jobs, err := db.Due(ctx, "A", now, 10)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("jobs=%v err=%v", jobs, err)
		}
		if err := db.Finish(ctx, jobs[0].ID, "pending", 0, now.Add(time.Minute), ""); err != nil {
			t.Fatal(err)
		}
		var events []string
		r := Runner{Config: config.Defaults(), Store: db, Account: "A", Windows: []schedule.Window{{Start: now, End: now.Add(time.Hour)}}, Scanner: scanFunc(func(context.Context) ([]ucloud.Notification, error) {
			events = append(events, "session failure")
			return nil, auth.ErrSession
		}), Sender: sendFunc(func(context.Context, string) error { events = append(events, "send"); return nil })}
		bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := r.Run(bounded); !errors.Is(err, auth.ErrSession) {
			t.Fatal(err)
		}
		events = append(events, "exit")
		if !reflect.DeepEqual(events, []string{"session failure", "send", "exit"}) {
			t.Fatal(events)
		}
	})
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestPollObservationAndDeadline(t *testing.T) {
	for _, timeout := range []time.Duration{time.Second, 3 * time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				db := testStore(t)
				ctx := context.Background()
				start := time.Now()
				if _, err := db.ApplyScan(ctx, "A", nil, start); err != nil {
					t.Fatal(err)
				}
				c := config.Defaults()
				c.Poll.Timeout = config.Duration(timeout)
				r := Runner{Config: c, Store: db, Account: "A", Scanner: scanFunc(func(context.Context) ([]ucloud.Notification, error) {
					time.Sleep(2 * time.Second)
					return []ucloud.Notification{{ID: "new", Type: "互动课堂", Model: "chat", URL: "TO_CLASS"}}, nil
				})}
				result, err := r.Poll(ctx)
				if timeout == time.Second {
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("result=%+v err=%v", result, err)
					}
					status, err := db.Status(ctx, "A")
					if err != nil || status.Seen != 0 {
						t.Fatalf("partial commit: %+v %v", status, err)
					}
				} else {
					if err != nil || result.Queued != 1 {
						t.Fatalf("result=%+v err=%v", result, err)
					}
					jobs, err := db.Due(ctx, "A", time.Now(), 10)
					if err != nil || len(jobs) != 1 {
						t.Fatalf("jobs=%+v err=%v", jobs, err)
					}
					want := start.Add(2*time.Second + c.Notify.TTL.Value())
					if !jobs[0].ExpiresAt.Equal(want) {
						t.Fatalf("expiry=%v want %v", jobs[0].ExpiresAt, want)
					}
				}
			})
		})
	}
}

func TestDispatchBatchDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := testStore(t)
		ctx := context.Background()
		start := time.Now()
		if _, err := db.ApplyScan(ctx, "A", nil, start); err != nil {
			t.Fatal(err)
		}
		records := []store.Observed{{ID: "1", Message: "first", ExpiresAt: start.Add(time.Hour)}, {ID: "2", Message: "second", ExpiresAt: start.Add(time.Hour)}}
		if _, err := db.ApplyScan(ctx, "A", records, start); err != nil {
			t.Fatal(err)
		}
		c := config.Defaults()
		c.Notify.Timeout = config.Duration(time.Second)
		sent := 0
		r := Runner{Config: c, Store: db, Account: "A", Sender: sendFunc(func(ctx context.Context, _ string) error { sent++; <-ctx.Done(); return ctx.Err() })}
		if err := r.Dispatch(ctx); err != nil {
			t.Fatal(err)
		}
		if sent != 1 || time.Since(start) != time.Second {
			t.Fatalf("sent=%d elapsed=%v", sent, time.Since(start))
		}
		status, err := db.Status(ctx, "A")
		if err != nil || status.Pending != 2 {
			t.Fatalf("jobs lost: %+v %v", status, err)
		}
		jobs, err := db.Due(ctx, "A", time.Now(), 10)
		if err != nil || len(jobs) != 1 || jobs[0].Attempts != 0 {
			t.Fatalf("unstarted job was retried: %+v %v", jobs, err)
		}
	})
}
