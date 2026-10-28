package ucloud

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/OneZ3r0/buptwc/internal/auth"
	"github.com/OneZ3r0/buptwc/internal/netutil"
)

type Notification struct {
	ID         netutil.ID `json:"id"`
	Type       string     `json:"type"`
	Model      string     `json:"newsModel"`
	URL        string     `json:"newsUrl"`
	Title      string     `json:"newsTitle"`
	Info       string     `json:"newsInfo"`
	CreateTime string     `json:"createTime"`
	CopyTime   string     `json:"newsCopyTime"`
}

func (n Notification) Matches() bool {
	return n.Type == "互动课堂" && n.Model == "chat" && n.URL == "TO_CLASS"
}

// Timestamp gives recipient copy time precedence. Unknown timestamps use first observation.
func (n Notification) Timestamp(loc *time.Location, observed time.Time) time.Time {
	for _, value := range []string{n.CopyTime, n.CreateTime} {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02 15:04"} {
			if t, err := time.ParseInLocation(layout, value, loc); err == nil {
				if t.After(observed) {
					return observed
				}
				return t
			}
		}
	}
	return observed
}

type Client struct {
	HTTP                          *http.Client
	BaseURL                       string
	Session                       auth.Session
	AccountID, Identity, TenantID string
	PageSize, MaxPages            int
	mu                            sync.Mutex
}

func (c *Client) Account() string {
	if c.AccountID != "" {
		return c.AccountID
	}
	return c.Session.UserID
}

// Scan returns only complete, consistent scans; no partial results escape on failure.
// Page order is unknown, so seeing an old ID never terminates pagination.
func (c *Client) Scan(ctx context.Context) ([]Notification, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.Session.Valid(time.Now()) {
		return nil, auth.ErrSession
	}
	if c.PageSize < 1 || c.MaxPages < 1 {
		return nil, fmt.Errorf("分页参数无效")
	}
	var all []Notification
	seen := map[netutil.ID]bool{}
	total, serverSize, serverPages := -1, 0, 0
	for current := 1; current <= c.MaxPages; current++ {
		u, err := url.Parse(c.BaseURL + "/ykt-basics/api/inform/news/list")
		if err != nil {
			return nil, fmt.Errorf("无效 uCloud 地址")
		}
		q := u.Query()
		q.Set("newsCopyPersonId", c.Account())
		q.Set("current", strconv.Itoa(current))
		q.Set("size", strconv.Itoa(c.PageSize))
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("无法构造通知请求")
		}
		req.Header.Set("Blade-Auth", c.Session.AccessToken)
		req.Header.Set("Authorization", auth.Authorization)
		req.Header.Set("Tenant-Id", c.TenantID)
		req.Header.Set("Identity", c.Identity)
		res, err := netutil.Do(c.HTTP, req)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Code    *int  `json:"code"`
			Success *bool `json:"success"`
			Data    *struct {
				Records []Notification `json:"records"`
				Total   *int           `json:"total"`
				Size    *int           `json:"size"`
				Current *int           `json:"current"`
				Pages   *int           `json:"pages"`
			} `json:"data"`
		}
		if res.StatusCode == 401 || res.StatusCode == 403 {
			res.Body.Close()
			return nil, auth.ErrSession
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			return nil, fmt.Errorf("通知列表 HTTP %d", res.StatusCode)
		}
		err = netutil.JSON(res.Body, &envelope)
		res.Body.Close()
		if err != nil {
			return nil, err
		}
		if envelope.Code != nil && (*envelope.Code == 401 || *envelope.Code == 403) {
			return nil, auth.ErrSession
		}
		if envelope.Code == nil || (*envelope.Code != 200 && *envelope.Code != 0) || envelope.Success != nil && !*envelope.Success {
			return nil, fmt.Errorf("通知列表业务状态异常")
		}
		d := envelope.Data
		if d == nil || d.Records == nil || d.Total == nil || d.Size == nil || d.Current == nil || d.Pages == nil {
			return nil, fmt.Errorf("通知列表缺少 records 或分页字段")
		}
		if *d.Total < 0 || *d.Size < 1 || *d.Size > 1000 || *d.Current != current || *d.Pages < 0 {
			return nil, fmt.Errorf("通知分页字段无效")
		}
		expectedPages := *d.Total / *d.Size
		if *d.Total%*d.Size != 0 {
			expectedPages++
		}
		if *d.Pages != expectedPages && !(*d.Total == 0 && *d.Pages == 1) {
			return nil, fmt.Errorf("通知总量与页数不一致")
		}
		if *d.Pages > c.MaxPages {
			return nil, fmt.Errorf("通知超过分页上限 %d，未提交扫描", c.MaxPages)
		}
		if total < 0 {
			total, serverSize, serverPages = *d.Total, *d.Size, *d.Pages
		} else if total != *d.Total || serverSize != *d.Size || serverPages != *d.Pages {
			return nil, fmt.Errorf("分页过程中列表发生变化，稍后重试")
		}
		expected := serverSize
		if remaining := total - len(all); remaining < expected {
			expected = remaining
		}
		if len(d.Records) != expected {
			return nil, fmt.Errorf("通知分页记录不完整")
		}
		for _, n := range d.Records {
			if n.ID == "" {
				return nil, fmt.Errorf("通知缺少 ID")
			}
			if seen[n.ID] {
				return nil, fmt.Errorf("分页出现重复 ID，稍后重新完整扫描")
			}
			seen[n.ID] = true
			all = append(all, n)
		}
		if len(all) == total {
			return all, nil
		}
	}
	return nil, fmt.Errorf("触及分页上限，未提交扫描")
}
