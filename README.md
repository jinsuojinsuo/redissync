# redissync

一、介绍

基于 redislock,go-redis 进行了二次封装，增肌自动续期功能



二、依赖

```
"github.com/bsm/redislock"
"github.com/go-redis/redis/v8"
```



三、安装

```
go get github.com/jinsuojinsuo/redissync 
```



四、示例

```go
package main

import (
	"github.com/go-redis/redis/v8"
	"github.com/jinsuojinsuo/redissync"
	"log"
	"time"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6387",
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	RedisSync := redissync.NewRedisSync(rdb).SetLogger(log.Default())
	//syncLock(RedisSync)
	tryLock(RedisSync)

	time.Sleep(time.Second * 30)
}

func tryLock(RedisSync *redissync.RedisSync) {
	lock, err := RedisSync.TryLock("lock:test2", time.Second*20)
	if err == redissync.ErrNotObtained {
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
func syncLock(RedisSync *redissync.RedisSync) {
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

```

