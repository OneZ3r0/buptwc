package tests

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OneZ3r0/buptwc/internal/auth"
	"github.com/OneZ3r0/buptwc/internal/config"
	"github.com/OneZ3r0/buptwc/internal/monitor"
	"github.com/OneZ3r0/buptwc/internal/notify"
	"github.com/OneZ3r0/buptwc/internal/store"
	"github.com/OneZ3r0/buptwc/internal/ucloud"
)

func TestMonitorBaselineRetryAndRestart(t *testing.T) {
	ctx := context.Background()
	var records atomic.Value
	records.Store([]ucloud.Notification{{ID: "old", Type: "互动课堂", Model: "chat", URL: "TO_CLASS", Title: "历史"}})
	var sent atomic.Int32
	var breakSecondPage atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/webhook" {
			var payload struct {
				MsgType string `json:"msgtype"`
				Text    struct {
					Content string `json:"content"`
				} `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.MsgType != "text" || payload.Text.Content == "" {
				t.Error("invalid WeChat payload")
			}
			code := 0
			if sent.Add(1) == 1 {
				code = 45009
			}
			json.NewEncoder(w).Encode(map[string]int{"errcode": code})
			return
		}
		if r.URL.Path != "/ykt-basics/api/inform/news/list" {
			http.NotFound(w, r)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("current"))
		ns := records.Load().([]ucloud.Notification)
		if breakSecondPage.Load() && page == 2 {
			w.WriteHeader(503)
			return
		}
		if page < 1 || page > len(ns) {
			t.Errorf("unexpected page %d", page)
			w.WriteHeader(400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"records": ns[page-1 : page], "total": len(ns), "size": 1, "current": page, "pages": len(ns)}})
	}))
	defer srv.Close()
	c := config.Defaults()
	c.Notify.RetryBase = config.Duration(time.Millisecond)
	c.Notify.RetryMax = c.Notify.RetryBase
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	client := &ucloud.Client{HTTP: srv.Client(), BaseURL: srv.URL, Session: auth.Session{AccessToken: "test-token", UserID: "A", ExpiresAt: time.Now().Add(time.Hour)}, PageSize: 1, MaxPages: 10}
	r := monitor.Runner{Config: c, Store: db, Scanner: client, Sender: &notify.Sender{HTTP: srv.Client(), Webhook: srv.URL + "/webhook"}, Account: "A", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	result, err := r.Poll(ctx)
	if err != nil || !result.Baseline || result.Queued != 0 {
		t.Fatalf("baseline: %+v %v", result, err)
	}
	records.Store([]ucloud.Notification{{ID: "old"}, {ID: "new", Type: "互动课堂", Model: "chat", URL: "TO_CLASS", Title: "新课"}, {ID: "unmatched", Type: "其他通知"}})
	breakSecondPage.Store(true)
	if _, err := r.Poll(ctx); err == nil {
		t.Fatal("incomplete scan accepted")
	}
	st, err := db.Status(ctx, "A")
	if err != nil || st.Seen != 1 || st.Pending != 0 {
		t.Fatalf("partial scan committed: %+v %v", st, err)
	}
	breakSecondPage.Store(false)
	result, err = r.Poll(ctx)
	if err != nil || result.New != 2 || result.Queued != 1 {
		t.Fatalf("new scan: %+v %v", result, err)
	}
	// No course windows are supplied: queued deliveries must work independently.
	if err := r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	st, err = db.Status(ctx, "A")
	if err != nil || st.Pending != 1 || st.Sent != 0 || sent.Load() != 1 {
		t.Fatalf("failed delivery lost: %+v %v", st, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Store = db
	wake, ok, err := db.NextJob(ctx, "A")
	if err != nil || !ok {
		t.Fatalf("missing recovered retry: %v", err)
	}
	time.Sleep(time.Until(wake)) // Wait until the persisted retry is due.
	if err := r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	result, err = r.Poll(ctx)
	if err != nil || result.New != 0 || result.Queued != 0 || result.Baseline {
		t.Fatalf("restart dedup: %+v %v", result, err)
	}
	if err := r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	st, err = db.Status(ctx, "A")
	if err != nil || st.Sent != 1 || st.Pending != 0 || sent.Load() != 2 {
		t.Fatalf("duplicate or missing delivery: %+v %v, sends=%d", st, err, sent.Load())
	}
	// A switched account cannot see or deliver the previous account's jobs.
	r.Account = "B"
	if err := r.Dispatch(ctx); err != nil || sent.Load() != 2 {
		t.Fatalf("cross-account delivery: %v", err)
	}
}
