package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
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
		w.Write([]byte(`{"access_token":"test-token","user_id":9007199254740993,"expires_in":3600}`))
	}))
	defer srv.Close()
	c := New(time.Second, "role", "tenant")
	c.CASURL = srv.URL + "/login"
	c.APIBase = srv.URL
	s, e := c.Login(context.Background(), "user", "test")
	if e != nil || s.UserID != "9007199254740993" || !s.Valid(time.Now()) {
		t.Fatalf("%+v %v", s, e)
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
