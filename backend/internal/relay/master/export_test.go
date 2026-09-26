package master

import "time"

// SetRuntimeClock 让外部测试包控制运行时的时钟（根证书停用等待）。
func SetRuntimeClock(r *Runtime, now func() time.Time) { r.now = now }
