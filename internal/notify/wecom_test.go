package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/OneZ3r0/buptwc/internal/ucloud"
)

func TestWeComResponseAndTextLimit(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{`{"errcode":0}`, true}, {`{"errcode":45009}`, false}, {`{}`, false}, {`not JSON`, false}, {`{"errcode":0} {}`, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					MsgType string `json:"msgtype"`
					Text    struct {
						Content       string   `json:"content"`
						MentionedList []string `json:"mentioned_list"`
					} `json:"text"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.MsgType != "text" || len(body.Text.Content) > 2048 || !utf8.ValidString(body.Text.Content) || !strings.HasSuffix(body.Text.Content, "…") {
					t.Error("invalid/truncated message")
				}
				if !strings.HasPrefix(body.Text.Content, "@所有人\n") || len(body.Text.MentionedList) != 1 || body.Text.MentionedList[0] != "@all" {
					t.Error("message must mention everyone")
				}
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			s := Sender{HTTP: srv.Client(), Webhook: srv.URL}
			err := s.Send(context.Background(), strings.Repeat("中文🙂", 500))
			if (err == nil) != tc.ok {
				t.Fatalf("got %v; want success %t", err, tc.ok)
			}
		})
	}
}

func TestMessageIsPlainText(t *testing.T) {
	message := Message(ucloud.Notification{Title: "<b>课程</b>", Info: "<script>private</script><p>签到<br>现在</p>", URL: "TO_CLASS"}, time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC))
	for _, bad := range []string{"<b>", "<script>", "private", "TO_CLASS"} {
		if strings.Contains(message, bad) {
			t.Errorf("message contains %q: %s", bad, message)
		}
	}
	if !strings.Contains(message, "签到 现在") {
		t.Fatalf("lost readable content: %s", message)
	}
}
