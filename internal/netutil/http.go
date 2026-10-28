package netutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Clients never forward secrets through redirects. CAS handles its redirect explicitly.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type transportError struct{ cause error }

func (e transportError) Error() string {
	return "网络请求失败（超时、连接或 TLS 错误）"
}
func (e transportError) Unwrap() error { return e.cause }

func Do(c *http.Client, req *http.Request) (*http.Response, error) {
	r, err := c.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, transportError{err}
	}
	return r, nil
}

func Read(r io.Reader) ([]byte, error) {
	const max = 4 << 20
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败")
	}
	if len(b) > max {
		return nil, fmt.Errorf("响应超过 4 MiB")
	}
	return b, nil
}

func JSON(r io.Reader, target any) error {
	b, err := Read(r)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("无效的 JSON 响应结构")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("JSON 响应有额外内容")
	}
	return nil
}

// ID preserves integer identifiers beyond JavaScript's 53-bit precision.
type ID string

func (id *ID) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, (*string)(id))
	}
	if len(b) == 0 {
		return fmt.Errorf("ID 不能为空")
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return fmt.Errorf("ID 必须为字符串或无符号整数")
		}
	}
	*id = ID(b)
	return nil
}

func Backoff(base, max time.Duration, attempt int) time.Duration {
	d := base
	for i := 1; i < attempt && d < max; i++ {
		if d > max/2 {
			return max
		}
		d *= 2
	}
	if d > max {
		return max
	}
	return d
}
