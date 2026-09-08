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
	_, file, line, ok := runtime.Caller(2)
	if ok == true {
		return fmt.Sprintf("%v:%v", pathRetainRight(file, s), line)
	} else {
		return ""
	}
}

// getExternalCaller 获取包外调用者或包内测试调用者所在行号
func getExternalCaller() string {
	pc, _, _, ok := runtime.Caller(0)
	if ok == false {
		return ""
	}
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return ""
	}
	funcName := fn.Name()
	packageName := strings.TrimSuffix(funcName, ".getExternalCaller")
	pcs := make([]uintptr, 16)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.HasPrefix(frame.Function, packageName+".") == false || strings.HasSuffix(frame.File, "_test.go") {
			return fmt.Sprintf("%v:%v", pathRetainRight(frame.File, 3), frame.Line)
		}
		if more == false {
			break
		}
	}
	return ""
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
