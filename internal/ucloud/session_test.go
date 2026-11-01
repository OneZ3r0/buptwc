package ucloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/OneZ3r0/buptwc/internal/auth"
)

func TestScanRefreshAndRestart(t *testing.T) {
	for _, scenario := range []string{"expired", "due", "http rejection", "business rejection", "still rejected"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now()
			s := auth.Session{AccessToken: "old-access", RefreshToken: "old-refresh", UserID: "user", IssuedAt: now, ExpiresAt: now.Add(3 * time.Hour)}
			if scenario == "expired" {
				s.IssuedAt, s.ExpiresAt = now.Add(-3*time.Hour), now.Add(-time.Second)
			}
			if scenario == "due" {
				s.IssuedAt, s.ExpiresAt = now.Add(-2*time.Hour), now.Add(time.Hour)
			}
			var pages []string
			refreshes := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/ykt-basics/oauth/token" {
					refreshes++
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					want := "old-refresh"
					if refreshes == 2 {
						want = "next-refresh"
					}
					if r.Form.Get("refresh_token") != want {
						t.Error("used stale refresh token")
					}
					fmt.Fprintf(w, `{"access_token":"next-access","refresh_token":"next-refresh","user_id":"user","expires_in":7199}`)
					return
				}
				page := r.URL.Query().Get("current")
				token := r.Header.Get("Blade-Auth")
				pages = append(pages, token+":"+page)
				if r.URL.Query().Get("newsCopyPersonId") != "recipient" {
					t.Error("lost account override")
				}
				if scenario == "still rejected" || token == "old-access" && page == "2" {
					if scenario == "business rejection" {
						fmt.Fprint(w, `{"code":401}`)
					} else {
						w.WriteHeader(401)
					}
					return
				}
				fmt.Fprintf(w, `{"code":200,"data":{"records":[{"id":%q}],"total":2,"size":1,"current":%s,"pages":2}}`, page, page)
			}))
			defer srv.Close()
			path := filepath.Join(t.TempDir(), "session.json")
			c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, Session: s, SessionPath: path, AccountID: "recipient", PageSize: 1, MaxPages: 10}
			got, err := c.Scan(ctx)
			if scenario == "still rejected" {
				if !errors.Is(err, auth.ErrSession) || got != nil || refreshes != 1 || len(pages) != 2 {
					t.Fatalf("retry did not stop: %v %d %v", err, refreshes, pages)
				}
				return
			}
			if err != nil || len(got) != 2 || refreshes != 1 {
				t.Fatalf("scan: %+v %v; refreshes %d", got, err, refreshes)
			}
			wantPages := []string{"next-access:1", "next-access:2"}
			if scenario == "http rejection" || scenario == "business rejection" {
				wantPages = append([]string{"old-access:1", "old-access:2"}, wantPages...)
			}
			if !reflect.DeepEqual(pages, wantPages) {
				t.Fatalf("pagination: %v", pages)
			}
			saved, err := auth.Load(path)
			if err != nil || saved.RefreshToken != "next-refresh" || saved.AccessToken != "next-access" {
				t.Fatalf("saved session: %+v %v", saved, err)
			}
			// A new client must use the token persisted by the previous process.
			restarted := &Client{HTTP: srv.Client(), BaseURL: srv.URL, Session: saved, SessionPath: path}
			if err := restarted.Refresh(ctx); err != nil || refreshes != 2 {
				t.Fatalf("restart: %v; refreshes %d", err, refreshes)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRefreshRetriesAndPersistsRotatedToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		now := time.Now()
		path := filepath.Join(t.TempDir(), "session.json")
		// A directory at the destination makes the atomic rename fail.
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		calls := 0
		c := &Client{BaseURL: "https://ucloud.invalid", SessionPath: path, Session: auth.Session{AccessToken: "expired", RefreshToken: "old-refresh", UserID: "user", IssuedAt: now.Add(-3 * time.Hour), ExpiresAt: now.Add(-time.Hour)}}
		c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("refresh_token") != "old-refresh" {
				t.Fatal("unexpected refresh token")
			}
			status, body := 503, "temporarily unavailable"
			if calls == 2 {
				status, body = 200, `{"access_token":"next-access","refresh_token":"next-refresh","expires_in":7199}`
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}
		if err := c.Refresh(ctx); err == nil || errors.Is(err, auth.ErrSession) {
			t.Fatalf("transient error: %v", err)
		}
		if got := c.NextRefresh(); !got.Equal(now.Add(5 * time.Second)) {
			t.Fatalf("retry at: %v", got)
		}
		if _, err := c.Scan(ctx); err == nil || calls != 1 {
			t.Fatal("scan bypassed refresh backoff")
		}
		time.Sleep(5 * time.Second)
		if err := c.Refresh(ctx); err == nil || c.Session.RefreshToken != "next-refresh" || calls != 2 {
			t.Fatalf("save failure lost rotated token: %v", err)
		}
		if got := c.NextRefresh(); !got.Equal(time.Now().Add(10 * time.Second)) {
			t.Fatalf("second retry at: %v", got)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Second)
		if err := c.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatal("save retry rotated the refresh token again")
		}
		saved, err := auth.Load(path)
		if err != nil || saved.RefreshToken != "next-refresh" || !c.NextRefresh().Equal(saved.RefreshAt()) {
			t.Fatalf("persistence recovery: %+v %v", saved, err)
		}
	})
}

func TestRefreshCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	c := &Client{BaseURL: "https://ucloud.invalid", Session: auth.Session{RefreshToken: "refresh", UserID: "user"}, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}}
	done := make(chan error, 1)
	go func() { done <- c.Refresh(ctx) }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
