package ucloud

import (
	"context"
	"fmt"
	"time"

	"github.com/OneZ3r0/buptwc/internal/auth"
	"github.com/OneZ3r0/buptwc/internal/netutil"
)

// NextRefresh includes retry backoff and is independent of course windows.
func (c *Client) NextRefresh() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nextRefresh()
}

func (c *Client) nextRefresh() time.Time {
	if !c.refreshRetryAt.IsZero() {
		return c.refreshRetryAt
	}
	return c.Session.RefreshAt()
}

func (c *Client) Refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refresh(ctx)
}

// The caller holds mu, serializing refresh-token rotation with complete scans.
func (c *Client) refresh(ctx context.Context) error {
	if c.refreshError != nil && time.Now().Before(c.refreshRetryAt) {
		return c.refreshError
	}
	failed := func(err error) error {
		c.refreshFailures++
		c.refreshError = err
		c.refreshRetryAt = time.Now().Add(netutil.Backoff(5*time.Second, 5*time.Minute, c.refreshFailures))
		return err
	}
	// After a disk failure, retry saving the rotated token before rotating again.
	if !c.sessionUnsaved || !c.Session.Valid(time.Now()) {
		client := auth.Client{HTTP: c.HTTP, APIBase: c.BaseURL, Identity: c.Identity, TenantID: c.TenantID}
		session, err := client.Refresh(ctx, c.Session)
		if err != nil {
			return failed(err)
		}
		// Keep the newest refresh token even if persistence temporarily fails.
		c.Session = session
		c.sessionUnsaved = true
	}
	if c.SessionPath != "" {
		if err := auth.Save(c.SessionPath, c.Session); err != nil {
			return failed(fmt.Errorf("保存续期会话失败: %w", err))
		}
	}
	c.sessionUnsaved = false
	c.refreshRetryAt = time.Time{}
	c.refreshError = nil
	c.refreshFailures = 0
	return nil
}
