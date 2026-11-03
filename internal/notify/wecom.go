package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OneZ3r0/buptwc/internal/netutil"
	"github.com/OneZ3r0/buptwc/internal/ucloud"
	"golang.org/x/net/html"
)

type Sender struct {
	HTTP    *http.Client
	Webhook string
}

func New(webhook string, timeout time.Duration) (*Sender, error) {
	u, err := url.Parse(webhook)
	if err != nil || u.Scheme != "https" || u.Host != "qyapi.weixin.qq.com" || u.Path != "/cgi-bin/webhook/send" || u.Query().Get("key") == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("请在配置指定的环境变量中设置有效的企业微信群机器人 Webhook")
	}
	return &Sender{netutil.Client(timeout), webhook}, nil
}

func (s *Sender) Send(ctx context.Context, message string) error {
	message = truncate("@所有人\n"+message, 2048)
	payload := struct {
		MsgType string `json:"msgtype"`
		Text    struct {
			Content       string   `json:"content"`
			MentionedList []string `json:"mentioned_list"`
		} `json:"text"`
	}{MsgType: "text"}
	payload.Text.Content = message
	payload.Text.MentionedList = []string{"@all"}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Webhook, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("无效 Webhook 地址")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := netutil.Do(s.HTTP, req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("企业微信 HTTP %d", res.StatusCode)
	}
	var data struct {
		ErrCode *int `json:"errcode"`
	}
	if err := netutil.JSON(res.Body, &data); err != nil {
		return err
	}
	if data.ErrCode == nil {
		return fmt.Errorf("企业微信响应缺少 errcode")
	}
	if *data.ErrCode != 0 {
		return fmt.Errorf("企业微信发送失败 (errcode=%d)", *data.ErrCode)
	}
	return nil
}

func Message(n ucloud.Notification, at time.Time) string {
	title := plain(n.Title)
	if title == "" {
		title = "互动课堂"
	}
	return truncate(fmt.Sprintf("[uCloud 上课提醒]\n%s\n%s\n时间：%s\n请打开 uCloud 查看互动课堂。", title, plain(n.Info), at.Format("2006-01-02 15:04:05")), 2048)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	b := []byte(s[:max-3])
	for !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b) + "…"
}

func plain(s string) string {
	t := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := false
	for {
		switch t.Next() {
		case html.ErrorToken:
			return strings.Join(strings.Fields(b.String()), " ")
		case html.TextToken:
			if !skip {
				b.Write(t.Text())
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			token := t.Token()
			if token.Data == "script" || token.Data == "style" {
				skip = true
			}
			if token.Data == "br" || token.Data == "p" || token.Data == "div" {
				b.WriteByte(' ')
			}
		case html.EndTagToken:
			token := t.Token()
			if token.Data == "script" || token.Data == "style" {
				skip = false
			}
			if token.Data == "p" || token.Data == "div" {
				b.WriteByte(' ')
			}
		}
	}
}
