package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoginExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			if r.Method == "GET" {
				http.SetCookie(w, &http.Cookie{Name: "cas", Value: "test"})
				w.Write([]byte(`<input name="execution" value="step">`))
				return
			}
			r.ParseForm()
			if r.Form.Get("execution") != "step" || r.Form.Get("password") != "test" {
				t.Error("bad login form")
			}
			if _, e := r.Cookie("cas"); e != nil {
				t.Error(e)
			}
			w.Header().Set("Location", "https://ucloud.bupt.edu.cn/?ticket=test-ticket")
			w.WriteHeader(302)
			return
		}
		r.ParseForm()
		if r.Form.Get("ticket") != "test-ticket" || r.Form.Get("grant_type") != "third" {
			t.Error("bad exchange")
		}
		w.Write([]byte(`{"access_token":"test-token","refresh_token":"refresh-token","user_id":9007199254740993,"expires_in":3600}`))
	}))
	defer srv.Close()
	c := New(time.Second, "role", "tenant")
	c.CASURL = srv.URL + "/login"
	c.APIBase = srv.URL
	s, e := c.Login(context.Background(), "user", "test")
	if e != nil || s.UserID != "9007199254740993" || s.RefreshToken != "refresh-token" || s.IssuedAt.IsZero() || !s.Valid(time.Now()) {
		t.Fatalf("%+v %v", s, e)
	}
}

func TestRefreshResponse(t *testing.T) {
	current := Session{AccessToken: "old-access", RefreshToken: "old-refresh", UserID: "9007199254740993", ExpiresAt: time.Now().Add(-time.Hour)}
	for _, tc := range []struct {
		name, body                  string
		status                      int
		wantRefresh                 string
		wantError, wantSessionError bool
	}{
		{name: "rotated", status: 200, body: `{"access_token":"next-access","refresh_token":"next-refresh","user_id":9007199254740993,"expires_in":7199}`, wantRefresh: "next-refresh"},
		{name: "unrotated and no user id", status: 200, body: `{"access_token":"next-access","expires_in":"7199"}`, wantRefresh: "old-refresh"},
		{name: "invalid grant", status: 400, body: `{"error":"invalid_grant","error_description":"old-refresh"}`, wantError: true, wantSessionError: true},
		{name: "invalid grant with 200", status: 200, body: `{"error":"invalid_grant"}`, wantError: true, wantSessionError: true},
		{name: "unauthorized", status: 401, body: "old-refresh", wantError: true, wantSessionError: true},
		{name: "forbidden", status: 403, body: "old-refresh", wantError: true, wantSessionError: true},
		{name: "business unauthorized", status: 200, body: `{"code":401}`, wantError: true, wantSessionError: true},
		{name: "account changed", status: 200, body: `{"access_token":"next-access","user_id":"different","expires_in":7199}`, wantError: true, wantSessionError: true},
		{name: "server failure", status: 503, body: "old-refresh", wantError: true},
		{name: "rate limited", status: 429, body: "old-refresh", wantError: true},
		{name: "malformed", status: 200, body: "old-refresh", wantError: true},
		{name: "missing expiry", status: 200, body: `{"access_token":"next-access"}`, wantError: true},
		{name: "business failure", status: 200, body: `{"success":false,"access_token":"next-access","expires_in":7199}`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Method != "POST" || r.URL.Path != "/ykt-basics/oauth/token" || r.URL.RawQuery != "" || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != current.RefreshToken || r.Form.Has("ticket") {
					t.Error("incorrect refresh request")
				}
				if r.Header.Get("Authorization") != Authorization || r.Header.Get("Identity") != "role" || r.Header.Get("Tenant-Id") != "tenant" {
					t.Error("missing refresh headers")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			c := New(time.Second, "role", "tenant")
			c.APIBase = srv.URL
			before := time.Now()
			s, err := c.Refresh(context.Background(), current)
			if (err != nil) != tc.wantError || errors.Is(err, ErrSession) != tc.wantSessionError {
				t.Fatalf("unexpected error: %v", err)
			}
			if err != nil {
				if strings.Contains(err.Error(), current.RefreshToken) || strings.Contains(err.Error(), "next-access") {
					t.Fatal("token leaked in error")
				}
				return
			}
			if s.AccessToken != "next-access" || s.RefreshToken != tc.wantRefresh || s.UserID != current.UserID || s.IssuedAt.Before(before) || !s.ExpiresAt.Equal(s.IssuedAt.Add(7199*time.Second)) {
				t.Fatalf("incorrect refreshed session: %+v", s)
			}
		})
	}
	c := New(time.Second, "", "")
	if _, err := c.Refresh(context.Background(), Session{}); !errors.Is(err, ErrSession) {
		t.Fatal(err)
	}
}

func TestRefreshSchedule(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name               string
		lifetime, interval time.Duration
	}{
		{"two hour cap", 3 * time.Hour, 2 * time.Hour},
		{"observed lifetime", 7199 * time.Second, 6899 * time.Second},
		{"short lifetime", time.Minute, 54 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Session{RefreshToken: "refresh", IssuedAt: now, ExpiresAt: now.Add(tc.lifetime)}
			if got := s.RefreshAt(); !got.Equal(now.Add(tc.interval)) {
				t.Fatalf("refresh at %v", got)
			}
		})
	}
	if at := (Session{ExpiresAt: now}).RefreshAt(); !at.IsZero() {
		t.Fatal("legacy session scheduled refresh")
	}
}

func TestSessionPersistenceAndLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	now := time.Now().UTC()
	s := Session{AccessToken: "access", RefreshToken: "refresh", UserID: "user", IssuedAt: now, ExpiresAt: now.Add(2 * time.Hour)}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got != s {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("session permissions: %v %v", info, err)
	}
	legacy := fmt.Sprintf(`{"access_token":"legacy","user_id":"user","expires_at":%q}`, now.Add(-time.Hour).Format(time.RFC3339Nano))
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = Load(path)
	if err != nil || got.AccessToken != "legacy" || got.RefreshToken != "" || got.Valid(now) {
		t.Fatalf("legacy: %+v %v", got, err)
	}
}

func TestTokenUsesEarlierJWTExpiry(t *testing.T) {
	expiry := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	jwt := "header." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, expiry.Unix()))) + ".signature"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"refresh","user_id":"user","expires_in":7199}`, jwt)
	}))
	defer srv.Close()
	c := New(time.Second, "", "")
	c.APIBase = srv.URL
	s, err := c.Exchange(context.Background(), "ticket")
	if err != nil || !s.ExpiresAt.Equal(expiry) || !s.RefreshAt().Before(expiry) {
		t.Fatalf("expiry: %+v %v", s, err)
	}
}

func TestAuthenticationFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		login      bool
	}{
		{"captcha", `<script>config.captcha = {}</script>`, true},
		{"missing execution", `<html></html>`, true},
		{"business error", `{"error":"invalid_grant"}`, false},
		{"missing expiry", `{"access_token":"token","user_id":"user"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tc.body)) }))
			defer srv.Close()
			c := New(time.Second, "role", "tenant")
			c.CASURL = srv.URL
			c.APIBase = srv.URL
			var err error
			if tc.login {
				_, err = c.Login(context.Background(), "user", "password")
			} else {
				_, err = c.Exchange(context.Background(), "ticket")
			}
			if err == nil {
				t.Fatal("invalid authentication accepted")
			}
		})
	}
}
