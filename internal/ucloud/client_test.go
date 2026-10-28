package ucloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/OneZ3r0/buptwc/internal/auth"
	"github.com/OneZ3r0/buptwc/internal/netutil"
)

func TestScanPagination(t *testing.T) {
	for _, scenario := range []string{"complete", "duplicate", "changed total", "missing records", "short page", "page limit", "http failure", "session failure", "business failure", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/ykt-basics/api/inform/news/list" || r.URL.Query().Get("newsCopyPersonId") != "recipient" || r.URL.Query().Get("size") != "2" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Blade-Auth") != "test-token" || r.Header.Get("Authorization") != auth.Authorization || r.Header.Get("Identity") != "role" || r.Header.Get("Tenant-Id") != "tenant" {
					t.Error("missing headers")
				}
				page, _ := strconv.Atoi(r.URL.Query().Get("current"))
				records := []map[string]any{{"id": json.Number("9007199254740993")}, {"id": "old"}}
				if page == 2 {
					records = []map[string]any{{"id": "new"}}
				}
				total, pages := 3, 2
				if scenario == "empty" {
					total, pages = 0, 0
					records = []map[string]any{}
				}
				if page == 2 {
					switch scenario {
					case "duplicate":
						records[0]["id"] = "old"
					case "changed total":
						total = 4
					case "short page":
						records = []map[string]any{}
					case "missing records":
						records = nil
					case "http failure":
						w.WriteHeader(500)
						return
					case "session failure":
						w.WriteHeader(401)
						return
					case "business failure":
						w.Write([]byte(`{"code":500,"success":false}`))
						return
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"code": 200, "success": true, "data": map[string]any{"records": records, "total": total, "size": 2, "current": page, "pages": pages}})
			}))
			defer srv.Close()
			c := Client{HTTP: srv.Client(), BaseURL: srv.URL, Session: auth.Session{AccessToken: "test-token", UserID: "user", ExpiresAt: time.Now().Add(time.Hour)}, AccountID: "recipient", Identity: "role", TenantID: "tenant", PageSize: 2, MaxPages: 10}
			if scenario == "page limit" {
				c.MaxPages = 1
			}
			got, err := c.Scan(context.Background())
			switch scenario {
			case "complete":
				if err != nil || len(got) != 3 || got[0].ID != "9007199254740993" || got[2].ID != "new" || calls != 2 {
					t.Fatalf("got %+v, %v, calls %d", got, err, calls)
				}
			case "empty":
				if err != nil || len(got) != 0 || calls != 1 {
					t.Fatalf("empty: %v, %v, calls %d", got, err, calls)
				}
			default:
				if err == nil || got != nil {
					t.Fatalf("partial scan escaped: %+v, %v", got, err)
				}
				if scenario == "session failure" && !errors.Is(err, auth.ErrSession) {
					t.Fatalf("wrong session error: %v", err)
				}
			}
		})
	}
}

func TestNotificationMatchingAndTime(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	n := Notification{ID: netutil.ID("1"), Type: "互动课堂", Model: "chat", URL: "TO_CLASS", CopyTime: "2026-09-07 09:50:00", CreateTime: "2026-09-07 09:00:00"}
	if !n.Matches() || !n.Timestamp(now.Location(), now).Equal(now.Add(-10*time.Minute)) {
		t.Fatal("match or copy-time precedence")
	}
	n.CopyTime = "invalid"
	if !n.Timestamp(now.Location(), now).Equal(now.Add(-time.Hour)) {
		t.Fatal("create-time fallback")
	}
	n.CreateTime = "2099-01-01T00:00:00Z"
	if !n.Timestamp(now.Location(), now).Equal(now) {
		t.Fatal("future time not clamped")
	}
	n.CreateTime = "invalid"
	if !n.Timestamp(now.Location(), now).Equal(now) {
		t.Fatal("observation fallback")
	}
	for _, field := range []*string{&n.Type, &n.Model, &n.URL} {
		old := *field
		*field = "other"
		if n.Matches() {
			t.Fatal("partial match accepted")
		}
		*field = old
	}
}
