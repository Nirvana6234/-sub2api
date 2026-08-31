package routes

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPawConfigResponseJSONContract(t *testing.T) {
	want := PawConfigResponse{
		Data: PawConfigData{
			User: PawUser{ID: 42, Name: "user", Email: "user@example.com"},
			Groups: []PawGroup{{
				ID: 7, Name: "Balanced", Description: "",
				Models: []PawModel{{
					ID: "model-id", Name: "Model Name", OwnedBy: "provider",
					Reasoning: PawReasoningCapability{Supported: true, Values: []string{"low", "medium", "high"}, Default: "medium"},
					Vision:    true, ImageGeneration: false, FileInput: true,
				}},
			}},
			Defaults: PawDefaults{GroupID: 7, ModelID: "model-id", Reasoning: "medium"},
		},
	}

	raw, err := json.Marshal(want)
	require.NoError(t, err)
	require.JSONEq(t, `{"data":{"user":{"id":42,"name":"user","email":"user@example.com"},"groups":[{"id":7,"name":"Balanced","description":"","models":[{"id":"model-id","name":"Model Name","owned_by":"provider","reasoning":{"supported":true,"values":["low","medium","high"],"default":"medium"},"vision":true,"image_generation":false,"file_input":true}]}],"defaults":{"group_id":7,"model_id":"model-id","reasoning":"medium"}}}`, string(raw))

	literalJSON := `{"data":{"user":{"id":42,"name":"user","email":"user@example.com"},"groups":[{"id":7,"name":"Balanced","description":"","models":[{"id":"model-id","name":"Model Name","owned_by":"provider","reasoning":{"supported":true,"values":["low","medium","high"],"default":"medium"},"vision":true,"image_generation":false,"file_input":true}]}],"defaults":{"group_id":7,"model_id":"model-id","reasoning":"medium"}}}`
	var got PawConfigResponse
	require.NoError(t, json.Unmarshal([]byte(literalJSON), &got))
	require.Equal(t, want, got)
}

func TestPawChatRequestJSONContract(t *testing.T) {
	want := PawChatRequest{
		GroupID: 7, ModelID: "model-id", Reasoning: "medium",
		Messages:    []PawChatMessage{{Role: "user", Content: "Hello"}},
		Stream:      true,
		Attachments: []PawAttachmentReference{{ID: "attachment-id"}},
	}

	raw, err := json.Marshal(want)
	require.NoError(t, err)
	require.JSONEq(t, `{"group_id":7,"model_id":"model-id","reasoning":"medium","messages":[{"role":"user","content":"Hello"}],"stream":true,"attachments":[{"id":"attachment-id"}]}`, string(raw))

	var got PawChatRequest
	require.NoError(t, json.Unmarshal([]byte(`{"group_id":7,"model_id":"model-id","reasoning":"medium","messages":[{"role":"user","content":"Hello"}],"stream":true,"attachments":[{"id":"attachment-id"}]}`), &got))
	require.Equal(t, want, got)
}

func TestPawAttachmentReferenceJSONContract(t *testing.T) {
	want := PawAttachmentReference{ID: "attachment-id"}
	raw, err := json.Marshal(want)
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"attachment-id"}`, string(raw))

	var got PawAttachmentReference
	require.NoError(t, json.Unmarshal([]byte(`{"id":"attachment-id"}`), &got))
	require.Equal(t, want, got)
}

func TestPawErrorJSONContract(t *testing.T) {
	cases := []struct {
		literal  string
		constant string
	}{
		{literal: "CONFIG_UNAVAILABLE", constant: PawErrorCodeConfigUnavailable},
		{literal: "GROUP_FORBIDDEN", constant: PawErrorCodeGroupForbidden},
		{literal: "MODEL_UNAVAILABLE", constant: PawErrorCodeModelUnavailable},
		{literal: "REASONING_UNSUPPORTED", constant: PawErrorCodeReasoningUnsupported},
		{literal: "ATTACHMENT_INVALID", constant: PawErrorCodeAttachmentInvalid},
		{literal: "QUOTA_EXCEEDED", constant: PawErrorCodeQuotaExceeded},
		{literal: "UPSTREAM_UNAVAILABLE", constant: PawErrorCodeUpstreamUnavailable},
		{literal: "AUTH_REQUIRED", constant: PawErrorCodeAuthRequired},
	}

	for _, tc := range cases {
		require.Equal(t, tc.literal, tc.constant)
		want := PawErrorResponse{Error: PawError{Code: tc.literal, Message: "message"}}
		raw, err := json.Marshal(want)
		require.NoError(t, err)

		var got PawErrorResponse
		require.NoError(t, json.Unmarshal([]byte(`{"error":{"code":"`+tc.literal+`","message":"message"}}`), &got))
		require.Equal(t, want, got)
		require.JSONEq(t, `{"error":{"code":"`+tc.literal+`","message":"message"}}`, string(raw))
	}
}
