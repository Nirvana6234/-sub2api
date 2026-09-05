package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestOpenAITestModelForAccountUsesLightweightModelForFreePlan(t *testing.T) {
	free := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"plan_type": "free",
		},
	}
	paid := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"plan_type": "plus",
		},
	}

	require.Equal(t, openai.FreeTestModel, OpenAITestModelForAccount(free))
	require.Equal(t, openai.DefaultTestModel, OpenAITestModelForAccount(paid))
}
