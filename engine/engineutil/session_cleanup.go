package engineutil

import (
	"context"
	"errors"
	"sync"
)

// These facts extend the existing per-session Client and its closeMu. They do
// not own execution, permits or service lifetime, and they never poll/replay.
// Unknown helper completion is deliberately sticky until a future exact owner
// extension reports those domains; absence from running is never a cleanup oracle.
func (c *Client) EnableSessionCleanupAccounting() {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	c.cleanupAccounting = true
}
func (c *Client) beginSessionResourceAccounting(covered bool) (func(error), error) {
	c.closeMu.Lock()
	if !c.cleanupAccounting {
		c.closeMu.Unlock()
		return func(error) {}, nil
	}
	if c.cleanupAccountingClosed {
		c.closeMu.Unlock()
		return nil, errors.New("session resource owner is closed")
	}
	c.cleanupAccountingActive++
	if !covered {
		c.cleanupAccountingUnknown = true
	}
	c.closeMu.Unlock()
	var once sync.Once
	return func(cleanupErr error) {
		once.Do(func() {
			c.closeMu.Lock()
			defer c.closeMu.Unlock()
			c.cleanupAccountingActive--
			if cleanupErr != nil {
				c.cleanupAccountingFailed = true
			}
		})
	}, nil
}

// ConfirmSessionResourceCleanup runs only after native request/cache/scope
// teardown barriers. It closes NEW acquisition, then proves all observed
// operations returned through their owning cleanup and permit-release defers.
// It neither cancels work nor releases an execution permit itself.
func (c *Client) ConfirmSessionResourceCleanup() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if !c.cleanupAccounting {
		return errors.New("native executor cleanup coverage unavailable")
	}
	c.cleanupAccountingClosed = true
	if c.cleanupAccountingActive != 0 || c.cleanupAccountingFailed || c.cleanupAccountingUnknown {
		return errors.New("native executor/helper cleanup remains uncertain")
	}
	return nil
}

// Existing helper APIs have varied streaming/close/error contracts. Until each
// actual helper owner provides positive cleanup, touching any such domain makes
// the release proof unavailable instead of guessing that returned means cleaned.
func (c *Client) observeUncoveredSessionResource() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.cleanupAccountingClosed {
		return errors.New("session resource owner is closed")
	}
	if c.cleanupAccounting {
		c.cleanupAccountingUnknown = true
	}
	return nil
}

// Every host-service helper passes this existing routing boundary, including Git
// SSH/capture/apply APIs. Returning from a helper is not terminal cleanup proof.
// Opts retains the single native caller resolver; this adds no transport owner.
func (c *Client) GetHostServiceCaller(ctx context.Context, id string) (SessionCaller, error) {
	if err := c.observeUncoveredSessionResource(); err != nil {
		return nil, err
	}
	return c.Opts.GetHostServiceCaller(ctx, id)
}
