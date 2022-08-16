package redissync

import (
	"github.com/go-redis/redis/v8"
	"log"
	"testing"
	"time"
)

func TestRedisSync_Lock(t *testing.T) {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6387",
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	RedisSync := NewRedisSync(rdb).SetLogger(log.Default())
	//syncLock(RedisSync)
	tryLock(RedisSync)

	time.Sleep(time.Second * 30)
}

func tryLock(RedisSync *RedisSync) {
	lock, err := RedisSync.TryLock("lock:test2", time.Second*20)
	if err == ErrNotObtained {
		log.Fatalln("加锁失败1", err)
	} else if err != nil {
		log.Fatalln("加锁失败2", err)
	}

	defer func() {
		if err := lock.Unlock(); err != nil {
			log.Fatalln("解锁失败", err)
		}
		log.Println("解锁成功")
	}()

	for i := 0; i < 30; i++ {
		log.Println("执行中", i)
		time.Sleep(time.Second * 1)
	}
}

// 同步锁
func syncLock(RedisSync *RedisSync) {
	lock, err := RedisSync.Lock("lock:test")
	if err != nil {
		log.Fatalln("加锁失败", err)
	}
	log.Println("加锁成功")

	defer func() {
		if err := lock.Unlock(); err != nil {
			log.Fatalln("解锁失败")
		}
		log.Println("解锁成功")
	}()

	for i := 0; i < 300; i++ {
		log.Println("执行中", i)
		time.Sleep(time.Second * 1)
	}
}
