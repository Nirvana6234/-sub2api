package relaynotify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Settings 读写系统设置（service.SettingRepository 满足）。
type Settings interface {
	GetValue(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// feishuSign 是飞书自定义机器人的签名校验：以"时间戳\n密钥"为 HMAC-SHA256 的密钥、对空串签名，再 Base64。
func feishuSign(timestamp, secret string) string {
	mac := hmac.New(sha256.New, []byte(timestamp+"\n"+secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

type feishuPayload struct {
	Timestamp string `json:"timestamp,omitempty"`
	Sign      string `json:"sign,omitempty"`
	MsgType   string `json:"msg_type"`
	Content   struct {
		Text string `json:"text"`
	} `json:"content"`
}

type feishuReply struct {
	Code       int    `json:"code"`
	StatusCode int    `json:"StatusCode"`
	Msg        string `json:"msg"`
}

// sendFeishu 发一条文本消息，失败重试（最多 3 次，间隔 2 秒、6 秒）。
func (n *Notifier) sendFeishu(ctx context.Context, cfg *runtimeConfig, text string) error {
	var err error
	for attempt := 0; attempt < feishuAttempts; attempt++ {
		if attempt > 0 {
			n.opts.Sleep(time.Duration(attempt*attempt*2) * time.Second)
		}
		if err = n.postFeishu(ctx, cfg, text); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return err
}

func (n *Notifier) postFeishu(ctx context.Context, cfg *runtimeConfig, text string) error {
	var p feishuPayload
	p.MsgType = "text"
	p.Content.Text = text
	if cfg.secret != "" {
		p.Timestamp = strconv.FormatInt(n.now().Unix(), 10)
		p.Sign = feishuSign(p.Timestamp, cfg.secret)
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.opts.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("feishu returned HTTP %d", resp.StatusCode)
	}
	var reply feishuReply
	if json.Unmarshal(raw, &reply) == nil && reply.Code != 0 {
		return fmt.Errorf("feishu rejected the message: code %d %s", reply.Code, reply.Msg)
	}
	return nil
}

// ErrFeishuNotConfigured：飞书渠道没有配置（或没打开）。
var ErrFeishuNotConfigured = errors.New("the feishu channel is not configured")

// SendTest 向飞书发一条测试消息（后台"测试发送"，失败原样报错，不改发邮件）。
func (n *Notifier) SendTest(ctx context.Context) error {
	cfg, err := n.runtime(ctx)
	if err != nil {
		return err
	}
	if !cfg.feishuConfigured() {
		return ErrFeishuNotConfigured
	}
	return n.postFeishu(ctx, cfg, "[提示] 主从分流通知测试：飞书渠道已连通")
}
