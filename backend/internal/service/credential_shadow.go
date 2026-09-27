package service

import (
	"context"
	"fmt"
)

// resolveCredentialAccount 解析影子账号到其母账号，用于凭据/Token 透传。
// - 普通账号（非影子）：直接返回自身。
// - 影子账号：通过 repo 取母账号，校验母账号存在且为 OpenAI OAuth 类型，否则返回错误。
// 设计为包级函数（非任何 service 的方法），以便 OpenAIGatewayService / OpenAIQuotaService /
// AccountUsageService 等不同接收者共享同一实现。
func resolveCredentialAccount(ctx context.Context, repo AccountRepository, account *Account) (*Account, error) {
	if account == nil || !account.IsShadow() {
		return account, nil
	}
	var parent *Account
	if p := credentialParentFromContext(ctx); p != nil && p.ID == *account.ParentAccountID {
		// 主从分流从节点：母账号随选号下发，放在请求 ctx 里（从节点没有账号仓储）。
		parent = p
	} else if repo == nil {
		return nil, fmt.Errorf("resolve spark shadow parent %d: no account repository", *account.ParentAccountID)
	} else {
		var err error
		parent, err = repo.GetByID(ctx, *account.ParentAccountID)
		if err != nil {
			return nil, fmt.Errorf("resolve spark shadow parent %d: %w", *account.ParentAccountID, err)
		}
	}
	if parent == nil {
		return nil, fmt.Errorf("spark shadow parent %d not found", *account.ParentAccountID)
	}
	// 防御:创建路径已禁二级影子(G6),此处再挡一层——畸形数据/手工 DB 写出的影子→影子链
	// 会让凭据解析停在无凭据的一级影子(只解一层),fail-closed 比静默返回坏母更安全(外审第6轮)。
	if parent.IsShadow() {
		return nil, fmt.Errorf("spark shadow parent %d is itself a shadow", parent.ID)
	}
	if !parent.IsOpenAIOAuth() {
		return nil, fmt.Errorf("spark shadow parent %d is not OpenAI OAuth", parent.ID)
	}
	return parent, nil
}

type credentialParentKey struct{}

// WithCredentialParent 把影子账号的母账号放进请求 ctx（主从分流从节点：母账号随选号下发）。
func WithCredentialParent(ctx context.Context, parent *Account) context.Context {
	return context.WithValue(ctx, credentialParentKey{}, parent)
}

func credentialParentFromContext(ctx context.Context) *Account {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(credentialParentKey{}).(*Account)
	return p
}

// CredentialAccount 返回账号实际用的凭据账号：影子账号为它的母账号，其余为自身（主从分流主节点给从节点下发母账号用）。
func (s *OpenAIGatewayService) CredentialAccount(ctx context.Context, account *Account) (*Account, error) {
	return resolveCredentialAccount(ctx, s.accountRepo, account)
}
