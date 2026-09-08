package redissync

import (
	"testing"
	"time"
)

func TestRedisSyncWaitsForRemoteOwnerRelease(t *testing.T) {
	rdb := newIntegrationRedisClient(t)
	const key = "redissync:test:wait-29-seconds"
	cleanupRedisKey(t, rdb, key)

	owner := NewRedisSync(rdb)
	waiter := NewRedisSync(rdb)
	ownerLock, err := owner.Lock(key)
	if err != nil {
		t.Fatalf("owner lock failed: %v", err)
	}
	ownerUnlocked := false
	t.Cleanup(func() {
		if ownerUnlocked == false {
			_ = ownerLock.Unlock()
		}
	})

	type waitResult struct {
		lock   *Lock
		err    error
		waited time.Duration
	}
	resultCh := make(chan waitResult, 1)
	startCh := make(chan time.Time, 1)
	go func() {
		startedAt := time.Now()
		startCh <- startedAt
		lock, err := waiter.Lock(key)
		resultCh <- waitResult{lock: lock, err: err, waited: time.Since(startedAt)}
	}()

	startedAt := <-startCh
	const expectedWait = 29*time.Second + 32*time.Millisecond
	time.Sleep(time.Until(startedAt.Add(expectedWait)))
	if err := ownerLock.Unlock(); err != nil {
		t.Fatalf("owner unlock failed: %v", err)
	}
	ownerUnlocked = true

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("waiter lock failed: %v", result.err)
		}
		defer result.lock.Unlock()
		t.Logf("Redis wait duration: %s", result.waited)
		if result.waited < expectedWait || result.waited > expectedWait+500*time.Millisecond {
			t.Fatalf("wait duration=%s, want [%s, %s]", result.waited, expectedWait, expectedWait+500*time.Millisecond)
		}
	case <-time.After(expectedWait + 2*time.Second):
		t.Fatal("waiter did not acquire after owner unlock")
	}
}
