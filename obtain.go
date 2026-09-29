package redissync

import (
	"context"
	"time"

	"github.com/bsm/redislock"
	"github.com/go-redis/redis/v8"
)

var obtainScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("pexpire", KEYS[1], ARGV[2])
end
if redis.call("set", KEYS[1], ARGV[1], "PX", ARGV[2], "NX") then
    return 1
end
return 0
`)

const cleanupObtainScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
end
return 0
`

// obtainRedisClient 保留 redislock 的 token、续期和解锁机制，只处理加锁结果不确定的情况。
type obtainRedisClient struct {
	redislock.RedisClient
	owner *RedisSync
}

func (c *obtainRedisClient) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	// value 是 redislock 生成的完整 token + metadata。客户端自动重试时，识别本次已写入的锁。
	acquired, err := obtainScript.Run(ctx, c.RedisClient, []string{key}, value, expiration.Milliseconds()).Int64()
	if err != nil {
		// 调用方超时并不代表 Redis 未执行写入。使用独立超时，仅清理本次锁值。
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		removed, cleanupErr := c.RedisClient.Eval(cleanupCtx, cleanupObtainScript, []string{key}, value).Int64()
		cancel()
		if cleanupErr != nil {
			c.owner.error("加锁失败后清理残留锁失败 key:%s err:%+v cleanup_err:%+v", key, err, cleanupErr)
		} else if removed > 0 {
			c.owner.info("加锁失败后清理残留锁成功 key:%s err:%+v", key, err)
		}
	}
	return redis.NewBoolResult(acquired == 1, err)
}
