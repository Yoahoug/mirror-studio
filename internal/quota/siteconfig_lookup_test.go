package quota

import (
	"context"
	"testing"
	"time"
)

// Quota settlement runs inside WithImmediate, so resolving the default daily
// quota there must not touch the pooled handle: the pool hands out a single
// connection, and a cache miss (TTL expiry, or a console config write
// invalidating the key) made the request wait for the connection its own
// transaction was holding — a permanent hang that also blocked every other
// database caller.
func TestFreezeResolvesConfigWithoutDeadlock(t *testing.T) {
	service, handle := newTestService(t, nil)
	userID := seedUser(t, handle, "creator")
	ctx := context.Background()

	insertConfigRow(t, handle, "site.default_daily_free_quota", "7")
	if _, err := service.Ensure(ctx, userID); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if snapshot := readAccount(t, handle, userID); snapshot.daily != 7 {
		t.Fatalf("account = %+v, want the site config quota of 7", snapshot)
	}
	// A console config write drops the cached row; the next settlement path
	// then has to read it again.
	service.site.Invalidate("site.default_daily_free_quota")

	done := make(chan error, 1)
	go func() {
		_, err := service.Freeze(ctx, userID, 1, "L-DEADLOCK-1")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("freeze: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Freeze hung on an in-transaction siteconfig read")
	}
}
