package redissync

import (
	"context"
	"flag"
	"fmt"
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
		DB:       0,  // use default DB
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

	wg := sync.WaitGroup{}
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(pid int) {
			defer wg.Done()
			for j := 0; j < 1000000; j++ {
				syncLock(RedisSync, pid, j)
			}
		}(i)
	}
	wg.Wait()
}

// 同步锁
func syncLock(RedisSync *RedisSync, pid int, i int) {
	lock, err := RedisSync.Lock("lock:test")
	if err != nil {
		log.Printf("加锁失败 pid:%d err:%+v", pid, err)
		return
	}
	log.Printf("加锁成功 pid:%d", pid)

	defer func() {
		if err := lock.Unlock(); err != nil {
			log.Printf("解锁失败 pid:%d ", pid)
			return
		}
		log.Printf("解锁成功 pid:%d", pid)
	}()

	log.Printf("执行中pid:%d i:%d", pid, i)
	time.Sleep(time.Second * 40)

}
