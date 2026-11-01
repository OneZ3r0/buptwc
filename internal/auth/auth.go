package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/OneZ3r0/buptwc/internal/netutil"
	"github.com/OneZ3r0/buptwc/internal/state"
	"golang.org/x/net/html"
)

const CASURL = "https://auth.bupt.edu.cn/authserver/login?service=https://ucloud.bupt.edu.cn"
const APIBase = "https://apiucloud.bupt.edu.cn"

// Public portal client identifier, as used by the uCloud web application.
const Authorization = "Basic cG9ydGFsOnBvcnRhbF9zZWNyZXQ="

var ErrSession = errors.New("uCloud 会话已失效，请运行 buptwc login 重新登录")

type Session struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	UserID       string    `json:"user_id"`
	IssuedAt     time.Time `json:"issued_at,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (s Session) Valid(now time.Time) bool {
	return s.AccessToken != "" && s.UserID != "" && now.Before(s.ExpiresAt)
}

// RefreshAt caps the interval at two hours, leaving time to retry before expiry.
func (s Session) RefreshAt() time.Time {
	if s.RefreshToken == "" {
		return time.Time{}
	}
	if s.IssuedAt.IsZero() {
		return s.ExpiresAt.Add(-5 * time.Minute)
	}
	margin := min(5*time.Minute, s.ExpiresAt.Sub(s.IssuedAt)/10)
	at := s.ExpiresAt.Add(-margin)
	if limit := s.IssuedAt.Add(2 * time.Hour); limit.Before(at) {
		at = limit
	}
	return at
}

func Save(path string, s Session) error {
	if !s.Valid(time.Now()) {
		return ErrSession
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return state.WritePrivate(path, b)
}

func Load(path string) (Session, error) {
	b, err := state.ReadPrivate(path)
	if err != nil {
		return Session{}, err
	}
	var s Session
	if json.Unmarshal(b, &s) != nil || s.AccessToken == "" || s.UserID == "" || s.ExpiresAt.IsZero() {
		return Session{}, fmt.Errorf("会话文件无效，请重新登录")
	}
	return s, nil
}

type Client struct {
	HTTP                                *http.Client
	CASURL, APIBase, Identity, TenantID string
}

func New(timeout time.Duration, identity, tenant string) *Client {
	h := netutil.Client(timeout)
	h.Jar, _ = cookiejar.New(nil)
	return &Client{h, CASURL, APIBase, identity, tenant}
}

func (c *Client) Login(ctx context.Context, username, password string) (Session, error) {
	if username == "" || password == "" {
		return Session{}, fmt.Errorf("用户名和密码不能为空")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.CASURL, nil)
	if err != nil {
		return Session{}, fmt.Errorf("无效 CAS 地址")
	}
	res, err := netutil.Do(c.HTTP, req)
	if err != nil {
		return Session{}, err
	}
	body, readErr := netutil.Read(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Session{}, fmt.Errorf("CAS 页面 HTTP %d", res.StatusCode)
	}
	if readErr != nil {
		return Session{}, readErr
	}
	if regexp.MustCompile(`config\.captcha\s*=\s*\{`).Match(body) {
		return Session{}, fmt.Errorf("CAS 要求验证码，请在浏览器完成认证后使用 login --ticket-stdin")
	}
	execution, err := executionValue(string(body))
	if err != nil {
		return Session{}, err
	}
	form := url.Values{"username": {username}, "password": {password}, "submit": {"LOGIN"}, "type": {"username_password"}, "execution": {execution}, "_eventId": {"submit"}}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.CASURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Session{}, fmt.Errorf("无效 CAS 地址")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", c.CASURL)
	res, err = netutil.Do(c.HTTP, req)
	if err != nil {
		return Session{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound && res.StatusCode != http.StatusSeeOther {
		return Session{}, fmt.Errorf("CAS 认证未完成 (HTTP %d)，请检查密码或验证码", res.StatusCode)
	}
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		return Session{}, fmt.Errorf("CAS 重定向无效")
	}
	ticket := location.Query().Get("ticket")
	if ticket == "" {
		return Session{}, fmt.Errorf("CAS 未返回 Ticket")
	}
	return c.Exchange(ctx, ticket)
}

func executionValue(body string) (string, error) {
	t := html.NewTokenizer(strings.NewReader(body))
	for {
		switch t.Next() {
		case html.ErrorToken:
			return "", fmt.Errorf("CAS 页面缺少 execution，登录页面可能已变化")
		case html.StartTagToken, html.SelfClosingTagToken:
			token := t.Token()
			if token.Data != "input" {
				continue
			}
			name, value := "", ""
			for _, a := range token.Attr {
				if a.Key == "name" {
					name = a.Val
				}
				if a.Key == "value" {
					value = a.Val
				}
			}
			if name == "execution" && value != "" {
				return value, nil
			}
		}
	}
}

func (c *Client) Exchange(ctx context.Context, ticket string) (Session, error) {
	if strings.TrimSpace(ticket) == "" {
		return Session{}, fmt.Errorf("Ticket 不能为空")
	}
	form := url.Values{"grant_type": {"third"}, "ticket": {ticket}}
	return c.token(ctx, form, Session{})
}

func (c *Client) Refresh(ctx context.Context, current Session) (Session, error) {
	if current.RefreshToken == "" {
		return Session{}, ErrSession
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {current.RefreshToken}}
	return c.token(ctx, form, current)
}

func (c *Client) token(ctx context.Context, form url.Values, current Session) (Session, error) {
	refresh := form.Get("grant_type") == "refresh_token"
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIBase+"/ykt-basics/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Session{}, fmt.Errorf("无效 Token 地址")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", Authorization)
	req.Header.Set("Tenant-Id", c.TenantID)
	req.Header.Set("Identity", c.Identity)
	res, err := netutil.Do(c.HTTP, req)
	if err != nil {
		return Session{}, err
	}
	defer res.Body.Close()
	if refresh && (res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden) {
		return Session{}, ErrSession
	}
	// OAuth invalid_grant is commonly returned with HTTP 400, not HTTP 200.
	if res.StatusCode != http.StatusOK && !(refresh && res.StatusCode == http.StatusBadRequest) {
		return Session{}, fmt.Errorf("Token 交换 HTTP %d", res.StatusCode)
	}
	var data struct {
		AccessToken  string     `json:"access_token"`
		RefreshToken string     `json:"refresh_token"`
		UserID       netutil.ID `json:"user_id"`
		ExpiresIn    netutil.ID `json:"expires_in"`
		Code         *int       `json:"code"`
		Success      *bool      `json:"success"`
		Error        string     `json:"error"`
	}
	if err := netutil.JSON(res.Body, &data); err != nil {
		return Session{}, err
	}
	if refresh && (data.Error == "invalid_grant" || data.Error == "invalid_token" || data.Code != nil && (*data.Code == 401 || *data.Code == 403)) {
		return Session{}, ErrSession
	}
	if refresh {
		if data.UserID == "" {
			data.UserID = netutil.ID(current.UserID)
		} else if string(data.UserID) != current.UserID {
			return Session{}, fmt.Errorf("续期返回了不同账号: %w", ErrSession)
		}
		if data.RefreshToken == "" {
			data.RefreshToken = current.RefreshToken
		}
	}
	if res.StatusCode != http.StatusOK || data.Error != "" || data.Code != nil && *data.Code != 200 && *data.Code != 0 || data.Success != nil && !*data.Success || data.AccessToken == "" || data.UserID == "" {
		return Session{}, fmt.Errorf("Token 交换失败或响应缺少会话字段")
	}
	now := time.Now()
	var expiry time.Time
	if seconds, err := strconv.ParseInt(string(data.ExpiresIn), 10, 64); err == nil && seconds > 0 && seconds <= 365*86400 {
		expiry = started.Add(time.Duration(seconds) * time.Second)
	}
	// The unverified JWT payload is only an expiry hint; it is never used to authenticate.
	parts := strings.Split(data.AccessToken, ".")
	if len(parts) == 3 {
		if b, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			var p struct {
				Exp int64 `json:"exp"`
			}
			if json.Unmarshal(b, &p) == nil && p.Exp > 0 {
				jwtExpiry := time.Unix(p.Exp, 0)
				if expiry.IsZero() || jwtExpiry.Before(expiry) {
					expiry = jwtExpiry
				}
			}
		}
	}
	s := Session{AccessToken: data.AccessToken, RefreshToken: data.RefreshToken, UserID: string(data.UserID), IssuedAt: started, ExpiresAt: expiry}
	if !s.Valid(now) {
		return Session{}, fmt.Errorf("Token 缺少有效的过期时间，请重新登录")
	}
	return s, nil
}
