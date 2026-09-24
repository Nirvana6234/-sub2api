package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsRechargeBlockedUser(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		setKey bool
		userID int64
		want   bool
	}{
		{name: "名单命中", value: `[6]`, setKey: true, userID: 6, want: true},
		{name: "名单含多人时命中其一", value: `[3,6,42]`, setKey: true, userID: 42, want: true},
		{name: "不在名单内放行", value: `[6]`, setKey: true, userID: 7, want: false},
		{name: "空数组放行", value: `[]`, setKey: true, userID: 6, want: false},
		// 以下三种都是"配置读不出来"，必须放行：充值是正常业务，
		// 配置缺失/损坏时把所有人都挡在外面是更严重的故障。
		{name: "未配置该项放行", setKey: false, userID: 6, want: false},
		{name: "空字符串放行", value: "", setKey: true, userID: 6, want: false},
		{name: "非法JSON放行", value: `{"oops":true}`, setKey: true, userID: 6, want: false},
		// 未认证/异常上下文
		{name: "userID为0放行", value: `[6]`, setKey: true, userID: 0, want: false},
		{name: "userID为负放行", value: `[6]`, setKey: true, userID: -1, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vals := map[string]string{}
			if tt.setKey {
				vals[SettingKeyRechargeBlockedUserIDs] = tt.value
			}
			svc := &SettingService{settingRepo: &fakeSettingRepo{vals: vals}}
			require.Equal(t, tt.want, svc.IsRechargeBlockedUser(context.Background(), tt.userID))
		})
	}
}

func TestGetRechargeBlockedUserIDs(t *testing.T) {
	svc := &SettingService{settingRepo: &fakeSettingRepo{vals: map[string]string{
		SettingKeyRechargeBlockedUserIDs: `[6,42]`,
	}}}
	require.Equal(t, []int64{6, 42}, svc.GetRechargeBlockedUserIDs(context.Background()))
}
