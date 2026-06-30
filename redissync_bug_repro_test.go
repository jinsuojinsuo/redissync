package redissync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bsm/redislock"
	"github.com/go-redis/redis/v8"
)

func TestLockContextCancelWhileWaitingLocalLockRollsBackCount(t *testing.T) {
	const key = "lock:ctx-cancel-local"

	s := &RedisSync{m: map[string]*t1{}}
	local := &t1{ch: make(chan struct{}, 1)}
	local.ch <- struct{}{}
	local.num.Store(1)
	s.m[key] = local

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	lock, err := s.LockContext(ctx, key)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline error, got lock=%v err=%v", lock, err)
	}
	if got := local.num.Load(); got != 1 {
		t.Fatalf("local waiter count leaked after canceled LockContext: got %d, want 1", got)
	}
}

func TestLockObtainErrorRemovesEmptyLocalLockEntry(t *testing.T) {
	const key = "lock:obtain-error"

	s := &RedisSync{
		redisLockClient: redislock.New(errorRedisClient{}),
		m:               map[string]*t1{},
	}

	lock, err := s.LockContext(context.Background(), key)
	if err == nil {
		t.Fatalf("expected obtain error, got lock=%v", lock)
	}
	if _, ok := s.m[key]; ok {
		t.Fatalf("local lock entry leaked after obtain error for key %q", key)
	}
}

type errorRedisClient struct{}

func (errorRedisClient) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	cmd := redis.NewBoolCmd(ctx)
	cmd.SetErr(errors.New("forced redis SetNX error"))
	return cmd
}

func (errorRedisClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	cmd.SetErr(errors.New("forced redis Eval error"))
	return cmd
}

func (errorRedisClient) EvalSha(ctx context.Context, sha1 string, keys []string, args ...interface{}) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	cmd.SetErr(errors.New("forced redis EvalSha error"))
	return cmd
}

func (errorRedisClient) ScriptExists(ctx context.Context, scripts ...string) *redis.BoolSliceCmd {
	cmd := redis.NewBoolSliceCmd(ctx)
	cmd.SetErr(errors.New("forced redis ScriptExists error"))
	return cmd
}

func (errorRedisClient) ScriptLoad(ctx context.Context, script string) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx)
	cmd.SetErr(errors.New("forced redis ScriptLoad error"))
	return cmd
}
