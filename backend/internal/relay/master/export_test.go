package master

import "time"

// HandoffKey 返回运行中的"交给主节点转发"标记密钥（测试用它给请求签名）。
func HandoffKey(r *Runtime) []byte {
	rr, err := r.runningRelay()
	if err != nil {
		return nil
	}
	return rr.handoffKey
}

// SetRuntimeClock 让外部测试包控制运行时的时钟（根证书停用等待）。
func SetRuntimeClock(r *Runtime, now func() time.Time) { r.now = now }
