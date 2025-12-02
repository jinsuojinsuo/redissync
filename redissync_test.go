package redissync

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/bsm/redislock"
	"github.com/go-redis/redis/v8"
	"log"
	"net/http"
	_ "net/http/pprof"
	"sync"
	"testing"
	"time"
)

type Loger struct {
}

func (s Loger) Info(format string, v ...any) {
	log.Printf(format, v...)
}

func (s Loger) Error(format string, v ...any) {
	log.Printf(format, v...)

}

var port = 0

func init() {
	flag.IntVar(&port, "port", 8080, "port")
}

// go test -run TestRedisSync_Lock
// go test -port 8001 -run TestRedisSync_Lock
// go test -port 8002 -run TestRedisSync_Lock
// http://127.0.0.1:8001/debug/pprof/
func TestRedisSync_Lock(t *testing.T) {

	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	rdb := redis.NewClient(&redis.Options{
		Addr:     "127.0.0.1:6379",
		Password: "", // no password set
		DB:       1,  // use default DB
	})

	go func() {
		if err := http.ListenAndServe(fmt.Sprintf(":%d", port), http.DefaultServeMux); err != nil {
			log.Fatal(err)
		}
	}()

	go func() {
		for i := 0; ; i++ {
			if err := rdb.Ping(context.Background()).Err(); err != nil {
				log.Printf("ping失败 %+v", err)
			} else {
				log.Printf("ping成功")
				time.Sleep(time.Second * 5)
			}
		}
	}()

	RedisSync := NewRedisSync(rdb).SetLogger(&Loger{})
	//Obtain(rdb)

	//syncLockContext(RedisSync)
	//syncLock(RedisSync)
	syncLockContext4(RedisSync)

	log.Println(RedisSync)
}

func syncLockContext4(RedisSync *RedisSync) {
	wg := sync.WaitGroup{}
	const key = "lock:test3"
	wg.Go(func() {

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
		defer cancel()
		lockContext, err := RedisSync.LockContext(ctx, key)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, redislock.ErrNotObtained) {
				log.Printf("超时取消加锁或尝试次数到上限 %+v", err) //超时取消也有可能会报错ErrNotObtained这点有些鸡肋啊 v0.9.2 已改版，但把redis升级到了v9我用不了啊
				return
			} else {
				log.Printf("加锁失败 %+v", err)
				return
			}
		}
		defer lockContext.Unlock()
		log.Println("加锁成功")
		time.Sleep(time.Second * 10)
	})
	wg.Go(func() {
		time.Sleep(time.Millisecond * 100)
		lockContext, err := RedisSync.Lock(key)
		if err != nil {
			log.Printf("加锁失败 %+v", err)
			return
		}
		defer lockContext.Unlock()
		log.Println("加锁成功")
		time.Sleep(time.Second * 3)

	})
	wg.Wait()
}

func Obtain(rdb *redis.Client) {
	wg := sync.WaitGroup{}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Obtain2(rdb)
		}()
	}
	wg.Wait()
}

// 原始锁
func Obtain2(rdb *redis.Client) {
	now := time.Now()
	log.Printf("当前时间 %v", now)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	rdlLock, err := redislock.New(rdb).Obtain(ctx, "lock:test3", time.Second*100, &redislock.Options{
		//重试策略 默认最多只等待ttl秒或设置 context.WithTimeout 来控制尝试时长
		RetryStrategy: redislock.LinearBackoff(time.Millisecond * 50), //100毫秒重试1次
		Metadata:      "metadata",
	})
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, redislock.ErrNotObtained) {
		log.Printf("超时取消加锁或尝试次数到上限 %+v", err)
		return
	} else if err != nil {
		log.Printf("加锁失败 %+v", err) //超时取消也有可能会报错ErrNotObtained这点有些鸡肋啊 v0.9.2 已改版
		return
	} else {
		log.Printf("加锁成功")
		defer rdlLock.Release(context.Background())
	}
	time.Sleep(time.Second * 5)
}

// 等锁超时取消加锁
func syncLockContext(RedisSync *RedisSync) {
	wg := sync.WaitGroup{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			syncLockContext2(RedisSync)
		}()
	}
	wg.Wait()
}

// 等锁超时取消加锁
func syncLockContext2(RedisSync *RedisSync) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	if lock, err := RedisSync.LockContext(ctx, "lock:test2"); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, redislock.ErrNotObtained) {
			log.Printf("超时取消加锁或尝试次数到上限 %+v", err) //超时取消也有可能会报错ErrNotObtained这点有些鸡肋啊 v0.9.2 已改版，但把redis升级到了v9我用不了啊
		} else {
			log.Printf("加锁失败 %+v", err)
		}
	} else {
		defer lock.Unlock()
	}
	time.Sleep(time.Second * 5)
}

// 同步锁
func syncLock(RedisSync *RedisSync) {
	wg := sync.WaitGroup{}
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(pid int) {
			defer wg.Done()
			for j := 0; j < 1000000; j++ {
				syncLock2(RedisSync, pid, j)
			}
		}(i)
	}
	wg.Wait()
}

// 同步锁
func syncLock2(RedisSync *RedisSync, pid int, i int) {
	lock, err := RedisSync.Lock("lock:test")
	if err != nil {
		log.Printf("加锁失败 pid:%d err:%+v", pid, err)
		return
	}
	log.Printf("加锁成功 pid:%d", pid)

	defer func() {
		if err := lock.Unlock(); err != nil {
			log.Printf("解锁失败 pid:%d err:%+v", pid, err)
			return
		}
		log.Printf("解锁成功 pid:%d", pid)
	}()

	log.Printf("执行中pid:%d i:%d", pid, i)
	time.Sleep(time.Millisecond)

}
