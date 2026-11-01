package tests

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/OneZ3r0/buptwc/internal/auth"
	"github.com/OneZ3r0/buptwc/internal/config"
	"github.com/OneZ3r0/buptwc/internal/monitor"
	"github.com/OneZ3r0/buptwc/internal/schedule"
	"github.com/OneZ3r0/buptwc/internal/store"
	"github.com/OneZ3r0/buptwc/internal/ucloud"
)

type refreshTransport func(*http.Request) (*http.Response, error)

func (f refreshTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func tokenResponse(status int, body string) (*http.Response, error) {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

func refreshRunner(t *testing.T, session auth.Session, transport refreshTransport) (*monitor.Runner, *ucloud.Client) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := &ucloud.Client{HTTP: &http.Client{Transport: transport}, BaseURL: "https://ucloud.invalid", Session: session, SessionPath: filepath.Join(dir, "session.json"), PageSize: 10, MaxPages: 10}
	r := &monitor.Runner{Config: config.Defaults(), Store: db, Scanner: c, Account: "user", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return r, c
}

func startRefreshRunner(t *testing.T, r *monitor.Runner) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	synctest.Wait()
	return cancel, done
}

func TestAutomaticRefreshOutsideWindowsAndRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		s := auth.Session{AccessToken: "access-0", RefreshToken: "refresh-0", UserID: "user", IssuedAt: now, ExpiresAt: now.Add(3 * time.Hour)}
		var refreshes atomic.Int32
		r, client := refreshRunner(t, s, func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/ykt-basics/oauth/token" {
				t.Error("polled notifications outside course windows")
				return nil, fmt.Errorf("unexpected notification request")
			}
			if err := req.ParseForm(); err != nil {
				t.Error(err)
				return nil, err
			}
			if req.Form.Get("refresh_token") != fmt.Sprintf("refresh-%d", refreshes.Load()) {
				t.Error("stale refresh token after rotation/restart")
			}
			count := refreshes.Add(1)
			return tokenResponse(200, fmt.Sprintf(`{"access_token":"access-%d","refresh_token":"refresh-%d","user_id":"user","expires_in":7199}`, count, count))
		})
		if err := auth.Save(client.SessionPath, s); err != nil {
			t.Fatal(err)
		}
		cancel, done := startRefreshRunner(t, r)
		time.Sleep(2*time.Hour - time.Second)
		synctest.Wait()
		if refreshes.Load() != 0 {
			t.Fatal("refreshed before two-hour cap")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if refreshes.Load() != 1 {
			t.Fatalf("refreshes=%d", refreshes.Load())
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		saved, err := auth.Load(client.SessionPath)
		if err != nil || saved.RefreshToken != "refresh-1" {
			t.Fatalf("persisted: %+v %v", saved, err)
		}
		time.Sleep(time.Hour)
		restarted := &ucloud.Client{HTTP: client.HTTP, BaseURL: client.BaseURL, Session: saved, SessionPath: client.SessionPath}
		r.Scanner = restarted
		// A distant course must not postpone session refresh either.
		r.Windows = []schedule.Window{{Start: time.Now().Add(24 * time.Hour), End: time.Now().Add(25 * time.Hour)}}
		cancel, done = startRefreshRunner(t, r)
		if refreshes.Load() != 1 {
			t.Fatal("restart reset the refresh schedule")
		}
		time.Sleep(time.Until(saved.RefreshAt()) - time.Second)
		synctest.Wait()
		if refreshes.Load() != 1 {
			t.Fatal("refreshed too early after restart")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if refreshes.Load() != 2 {
			t.Fatalf("refreshes after restart=%d", refreshes.Load())
		}
		time.Sleep(6899 * time.Second)
		synctest.Wait()
		if refreshes.Load() != 3 {
			t.Fatalf("recurring refreshes=%d", refreshes.Load())
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestAutomaticRefreshRecoversAfterOutage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		s := auth.Session{AccessToken: "expired", RefreshToken: "refresh", UserID: "user", IssuedAt: now.Add(-3 * time.Hour), ExpiresAt: now.Add(-time.Hour)}
		var calls, polls atomic.Int32
		r, _ := refreshRunner(t, s, func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/ykt-basics/oauth/token" {
				polls.Add(1)
				if req.Header.Get("Blade-Auth") != "new-access" {
					t.Error("used expired access token")
				}
				return tokenResponse(200, `{"code":200,"data":{"records":[],"total":0,"size":10,"current":1,"pages":0}}`)
			}
			if calls.Add(1) <= 2 {
				return tokenResponse(503, "temporary outage")
			}
			return tokenResponse(200, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":7199}`)
		})
		r.Windows = []schedule.Window{{Start: now, End: now.Add(time.Hour)}}
		cancel, done := startRefreshRunner(t, r)
		if calls.Load() != 1 || polls.Load() != 0 {
			t.Fatalf("startup: calls=%d polls=%d", calls.Load(), polls.Load())
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("first retry: %d", calls.Load())
		}
		time.Sleep(9 * time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("refresh backoff ignored by polling")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.Load() != 3 || polls.Load() != 1 {
			t.Fatalf("recovery: calls=%d polls=%d", calls.Load(), polls.Load())
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

type refreshSender func(context.Context, string) error

func (f refreshSender) Send(ctx context.Context, body string) error { return f(ctx, body) }

func TestRefreshRejectionDrainsPendingNotifications(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		s := auth.Session{AccessToken: "expired", RefreshToken: "revoked", UserID: "user", IssuedAt: now.Add(-3 * time.Hour), ExpiresAt: now.Add(-time.Hour)}
		calls, sent := 0, 0
		r, _ := refreshRunner(t, s, func(req *http.Request) (*http.Response, error) {
			calls++
			if req.URL.Path != "/ykt-basics/oauth/token" {
				t.Error("scanned with revoked session")
			}
			return tokenResponse(400, `{"error":"invalid_grant"}`)
		})
		ctx := context.Background()
		if _, err := r.Store.ApplyScan(ctx, "user", nil, now); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Store.ApplyScan(ctx, "user", []store.Observed{{ID: "new", Message: "pending", ExpiresAt: now.Add(time.Hour)}}, now); err != nil {
			t.Fatal(err)
		}
		jobs, err := r.Store.Due(ctx, "user", now, 10)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("jobs: %v %v", jobs, err)
		}
		if err := r.Store.Finish(ctx, jobs[0].ID, "pending", 0, now.Add(time.Minute), ""); err != nil {
			t.Fatal(err)
		}
		r.Sender = refreshSender(func(context.Context, string) error { sent++; return nil })
		bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := r.Run(bounded); !errors.Is(err, auth.ErrSession) {
			t.Fatal(err)
		}
		if calls != 1 || sent != 1 || time.Since(now) != time.Minute {
			t.Fatalf("calls=%d sent=%d elapsed=%v", calls, sent, time.Since(now))
		}
	})
}
