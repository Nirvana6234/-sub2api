package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// OpenAITestModelForAccount selects a lightweight model for known Free
// accounts. Paid accounts keep the platform's normal verification model.
func OpenAITestModelForAccount(account *Account) string {
	if account != nil && account.IsOpenAIOAuth() && strings.EqualFold(strings.TrimSpace(account.GetCredential("plan_type")), "free") {
		return openai.FreeTestModel
	}
	return openai.DefaultTestModel
}
