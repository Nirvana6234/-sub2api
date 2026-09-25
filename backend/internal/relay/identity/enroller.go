package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
)

// ErrRejected 表示管理员拒绝了这把长期密钥，同一台机器要换密钥重新注册（设计 11.2）。
var ErrRejected = errors.New("relay node registration was rejected; generate a new long-term key and register again")

// EnrollerOptions 是注册时上报的信息（设计 11.1 第 3 步）。
type EnrollerOptions struct {
	Hostname       string
	ProgramVersion string
	DisplayName    string
	SystemInfo     map[string]string
	// OnStatus 在节点状态变化时调用（打印"等待激活，指纹 xxx"之类）。
	OnStatus func(relayv1.NodeStatus)
	// RenewGrace 是续签后丢掉旧加密私钥之前等待的时间，与主节点关闭旧证书连接的宽限一致（默认 60 秒）。
	RenewGrace time.Duration
}

// Enroller 让从节点拿到并保持一张有效的主从通信证书。
type Enroller struct {
	id     *Identity
	client *transport.Client
	opts   EnrollerOptions
}

// NewEnroller 创建注册流程。client 的证书回调必须是 id.TLSCertificate。
func NewEnroller(id *Identity, client *transport.Client, opts EnrollerOptions) *Enroller {
	if opts.RenewGrace <= 0 {
		opts.RenewGrace = 60 * time.Second
	}
	return &Enroller{id: id, client: client, opts: opts}
}

func (e *Enroller) enrollment() relayv1.RelayEnrollmentClient {
	return relayv1.NewRelayEnrollmentClient(e.client.Conn(transport.TierControl))
}

// EnsureCertificate 在没有有效证书时：注册、等管理员激活、领取证书。
// 已有有效证书时直接返回。ctx 结束或被拒绝时返回错误。
func (e *Enroller) EnsureCertificate(ctx context.Context) error {
	if e.id.HasValidIssued() {
		return nil
	}
	// 手里没有有效证书，连接必须用长期密钥证书重建。
	if err := e.client.Rotate(); err != nil {
		return err
	}
	if _, err := e.enrollment().Register(ctx, &relayv1.RegisterRequest{
		Hostname:       e.opts.Hostname,
		ProgramVersion: e.opts.ProgramVersion,
		DisplayName:    e.opts.DisplayName,
		SystemInfo:     e.opts.SystemInfo,
	}); err != nil {
		return fmt.Errorf("relay registration failed: %w", err)
	}

	last := relayv1.NodeStatus_NODE_STATUS_UNSPECIFIED
	backoff := transport.DefaultBackoff()
	for {
		resp, err := e.enrollment().NodeStatus(ctx, &relayv1.NodeStatusRequest{ProgramVersion: e.opts.ProgramVersion})
		wait := backoff.Next()
		if err == nil {
			backoff.Reset()
			if resp.Status != last {
				last = resp.Status
				if e.opts.OnStatus != nil {
					e.opts.OnStatus(resp.Status)
				}
			}
			switch resp.Status {
			case relayv1.NodeStatus_NODE_STATUS_ACTIVE, relayv1.NodeStatus_NODE_STATUS_DRAINING:
				return e.obtain(ctx)
			case relayv1.NodeStatus_NODE_STATUS_REJECTED:
				return ErrRejected
			case relayv1.NodeStatus_NODE_STATUS_UNKNOWN:
				// 待激活超时被清除了：重新注册。
				if _, err := e.enrollment().Register(ctx, &relayv1.RegisterRequest{
					Hostname: e.opts.Hostname, ProgramVersion: e.opts.ProgramVersion,
					DisplayName: e.opts.DisplayName, SystemInfo: e.opts.SystemInfo,
				}); err != nil {
					return fmt.Errorf("relay registration failed: %w", err)
				}
			}
			wait = time.Duration(resp.HeartbeatIntervalMs) * time.Millisecond
			if wait <= 0 {
				wait = 5 * time.Second
			}
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (e *Enroller) obtain(ctx context.Context) error {
	req, pending, err := e.id.PrepareRequest()
	if err != nil {
		return err
	}
	resp, err := e.enrollment().ObtainCertificate(ctx, req)
	if err != nil {
		return fmt.Errorf("relay certificate request failed: %w", err)
	}
	if err := e.id.Install(pending, resp); err != nil {
		return err
	}
	return e.client.Rotate()
}

// Renew 用当前证书续签一次：换新密钥、启用新证书、重建连接。
func (e *Enroller) Renew(ctx context.Context) error {
	req, pending, err := e.id.PrepareRequest()
	if err != nil {
		return err
	}
	resp, err := e.enrollment().RenewCertificate(ctx, req)
	if err != nil {
		return fmt.Errorf("relay certificate renewal failed: %w", err)
	}
	if err := e.id.Install(pending, resp); err != nil {
		return err
	}
	if err := e.client.Rotate(); err != nil {
		return err
	}
	time.AfterFunc(e.opts.RenewGrace, e.id.DropPreviousEncryptionKey)
	return nil
}

// RunRenewal 在证书用掉三分之二有效期时续签；失败按退避重试，证书过期就走恢复
// （EnsureCertificate 用长期密钥重新领取）。直到 ctx 结束。
func (e *Enroller) RunRenewal(ctx context.Context, onError func(error)) {
	backoff := transport.DefaultBackoff()
	for {
		if !e.id.HasValidIssued() {
			if err := e.EnsureCertificate(ctx); err != nil {
				if ctx.Err() != nil || errors.Is(err, ErrRejected) {
					if onError != nil && ctx.Err() == nil {
						onError(err)
					}
					return
				}
				if onError != nil {
					onError(err)
				}
				if !sleep(ctx, backoff.Next()) {
					return
				}
				continue
			}
		}
		notBefore, notAfter := e.id.IssuedValidity()
		renewAt := notBefore.Add(notAfter.Sub(notBefore) * 2 / 3)
		if !sleep(ctx, time.Until(renewAt)) {
			return
		}
		if err := e.Renew(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			if onError != nil {
				onError(err)
			}
			if !sleep(ctx, backoff.Next()) {
				return
			}
			continue
		}
		backoff.Reset()
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
