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

// HeartbeatsOf 返回运行中的心跳跟踪（测试里模拟节点的心跳）。
func HeartbeatsOf(r *Runtime) *Heartbeats {
	rr, err := r.runningRelay()
	if err != nil {
		return nil
	}
	return rr.heartbeats
}

// SetUserAssignRand 换掉小白端分配用的随机数（测试里让掷骰子可预期）。
func SetUserAssignRand(r *Runtime, rnd func() float64) {
	if rr, err := r.runningRelay(); err == nil && rr.users != nil {
		rr.users.rnd = rnd
	}
}

// UserAssignmentStoreOf 返回运行中的分配存储。
func UserAssignmentStoreOf(r *Runtime) UserAssignmentStore {
	if rr, err := r.runningRelay(); err == nil && rr.users != nil {
		return rr.users.store
	}
	return nil
}

// HealthOf 返回运行中的外部健康监控（测试里直接触发一轮检查）。
func HealthOf(r *Runtime) *HealthMonitor {
	rr, err := r.runningRelay()
	if err != nil {
		return nil
	}
	return rr.health
}
