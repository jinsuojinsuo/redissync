package redissync

import (
	"fmt"
	"path"
	"runtime"
	"strings"
)

// GetParentCaller 获取父级调用者所在行号
func getParentCaller(dirLevel ...int) string {
	s := 2 //默认为父级调用者
	if len(dirLevel) > 0 {
		s = dirLevel[0]
	}
	_, file, line, ok := runtime.Caller(3)
	if ok == true {
		return fmt.Sprintf("%v:%v", pathRetainRight(file, s), line)
	} else {
		return ""
	}
}

// PathRetainRight 保留n级目录
func pathRetainRight(pathStr string, n int) string {
	rt := ""
	for i := 0; i < n; i++ {
		b := path.Base(pathStr)
		if b == "." {
			break
		}
		rt = b + "/" + rt
		pathStr = path.Dir(pathStr)
	}
	return strings.Trim(rt, "/")
}
