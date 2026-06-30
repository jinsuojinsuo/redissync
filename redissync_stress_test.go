package redissync

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func TestRedisSyncStressNoDeadlockAndNoLocalLeak(t *testing.T) {
	rdb := newIntegrationRedisClient(t)
	const key = "redissync:test:stress:no-deadlock"
	cleanupRedisKey(t, rdb, key)

	s := NewRedisSync(rdb)
	var active int64
	var maxActive int64
	var stopped atomic.Bool
	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	for worker := 0; worker < 10; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				if stopped.Load() {
					return
				}

				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				lock, err := s.LockContext(ctx, key)
				cancel()
				if err != nil {
					reportStressError(errCh, &stopped, fmt.Errorf("worker %d iteration %d lock failed: %w", worker, i, err))
					return
				}

				now := atomic.AddInt64(&active, 1)
				recordMaxInt64(&maxActive, now)
				if now > 1 {
					reportStressError(errCh, &stopped, fmt.Errorf("local critical section overlapped: active=%d", now))
				}
				time.Sleep(time.Millisecond)
				atomic.AddInt64(&active, -1)

				if err := callUnlockWithTimeout(lock, 3*time.Second); err != nil {
					reportStressError(errCh, &stopped, fmt.Errorf("worker %d iteration %d unlock failed: %w", worker, i, err))
					return
				}
			}
		}(worker)
	}

	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		wg.Wait()
	}()
	select {
	case <-waitDone:
	case err := <-errCh:
		stopped.Store(true)
		t.Fatal(err)
	case <-time.After(30 * time.Second):
		stopped.Store(true)
		t.Fatalf("stress test timed out; possible deadlock\n%s", goroutineDump())
	}

	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
	if maxActive > 1 {
		t.Fatalf("critical section overlapped, max active=%d", maxActive)
	}
	if keys := s.GetM(); len(keys) != 0 {
		t.Fatalf("local lock map leaked after stress: %v", keys)
	}
}

func TestRedisSyncDoubleUnlockDoesNotCorruptNextOwner(t *testing.T) {
	rdb := newIntegrationRedisClient(t)
	const key = "redissync:test:double-unlock"
	cleanupRedisKey(t, rdb, key)

	s := NewRedisSync(rdb)
	first, err := s.Lock(key)
	if err != nil {
		t.Fatalf("first lock failed: %v", err)
	}

	secondReady := make(chan *Lock, 1)
	secondErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		second, err := s.LockContext(ctx, key)
		if err != nil {
			secondErr <- err
			return
		}
		secondReady <- second
	}()

	unlockWithTimeout(t, first, "first unlock")

	var second *Lock
	select {
	case second = <-secondReady:
	case err := <-secondErr:
		t.Fatalf("second lock failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("second lock did not acquire after first unlock")
	}

	err = callUnlockWithTimeout(first, 3*time.Second)
	if err == nil {
		t.Fatal("second unlock of the same Lock unexpectedly succeeded")
	}

	err = callUnlockWithTimeout(second, 3*time.Second)
	if err != nil {
		t.Fatalf("second owner unlock failed after first lock was unlocked twice: %v", err)
	}
	if keys := s.GetM(); len(keys) != 0 {
		t.Fatalf("local lock map leaked after double-unlock scenario: %v", keys)
	}
}

func newIntegrationRedisClient(t *testing.T) *redis.Client {
	t.Helper()

	addr := os.Getenv("REDISSYNC_REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	password := os.Getenv("REDISSYNC_REDIS_PASSWORD")
	db := 1

	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		t.Skipf("redis is not available at %s: %v", addr, err)
	}
	t.Cleanup(func() {
		_ = rdb.Close()
	})
	return rdb
}

func cleanupRedisKey(t *testing.T, rdb *redis.Client, key string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Del(ctx, key).Err(); err != nil {
		t.Fatalf("cleanup redis key %q failed: %v", key, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = rdb.Del(ctx, key).Err()
	})
}

func unlockWithTimeout(t *testing.T, lock *Lock, name string) {
	t.Helper()

	if err := callUnlockWithTimeout(lock, 3*time.Second); err != nil {
		t.Fatalf("%s failed: %v", name, err)
	}
}

func callUnlockWithTimeout(lock *Lock, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- lock.Unlock()
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("unlock timed out after %s", timeout)
	}
}

func recordMaxInt64(max *int64, value int64) {
	for {
		old := atomic.LoadInt64(max)
		if value <= old {
			return
		}
		if atomic.CompareAndSwapInt64(max, old, value) {
			return
		}
	}
}

func reportStressError(errCh chan<- error, stopped *atomic.Bool, err error) {
	if !stopped.CompareAndSwap(false, true) {
		return
	}
	errCh <- err
}

func goroutineDump() string {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return string(buf[:n])
}
