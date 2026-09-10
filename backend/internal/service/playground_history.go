package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	// PlaygroundHistoryVersion matches the browser persistence format. Keep this
	// stable so older browser state can be uploaded without a second migration.
	PlaygroundHistoryVersion = 2
	// Keep the server limit aligned with the browser's bounded history snapshot
	// while allowing normal long-form assistant replies to be restored intact.
	PlaygroundHistoryMaxBytes         = 4 * 1024 * 1024
	PlaygroundHistoryMaxMessageChars  = 1_000_000
	playgroundHistoryMaxProjects      = 24
	playgroundHistoryMaxConversations = 48
	playgroundHistoryMaxMessages      = 48
)

var ErrPlaygroundHistoryUnavailable = infraerrors.New(
	500,
	"PLAYGROUND_HISTORY_UNAVAILABLE",
	"Playground history is unavailable",
)

// PlaygroundHistoryRepository persists one Playground snapshot per user. API
// key ownership is intentionally outside this boundary: history is shared by
// the chat and image workspaces regardless of the active key.
type PlaygroundHistoryRepository interface {
	Get(ctx context.Context, userID int64) (json.RawMessage, error)
	Save(ctx context.Context, userID int64, state json.RawMessage, updatedAt time.Time) error
}

type PlaygroundHistoryService struct {
	repo PlaygroundHistoryRepository
}

func NewPlaygroundHistoryService(repo PlaygroundHistoryRepository) *PlaygroundHistoryService {
	return &PlaygroundHistoryService{repo: repo}
}

func (s *PlaygroundHistoryService) Get(ctx context.Context, userID int64) (json.RawMessage, error) {
	if s == nil || s.repo == nil {
		return nil, ErrPlaygroundHistoryUnavailable
	}
	if userID <= 0 {
		return nil, invalidPlaygroundHistory("invalid history scope")
	}
	return s.repo.Get(ctx, userID)
}

func (s *PlaygroundHistoryService) Save(ctx context.Context, userID int64, state json.RawMessage) error {
	if s == nil || s.repo == nil {
		return ErrPlaygroundHistoryUnavailable
	}
	if userID <= 0 {
		return invalidPlaygroundHistory("invalid history scope")
	}

	normalized, err := validatePlaygroundHistoryState(state)
	if err != nil {
		return err
	}
	return s.repo.Save(ctx, userID, normalized, time.Now().UTC())
}

func validatePlaygroundHistoryState(state json.RawMessage) (json.RawMessage, error) {
	if len(state) == 0 || len(state) > PlaygroundHistoryMaxBytes {
		return nil, invalidPlaygroundHistory("state exceeds the allowed size")
	}

	var source map[string]json.RawMessage
	if err := json.Unmarshal(state, &source); err != nil {
		return nil, invalidPlaygroundHistory("state must be a JSON object")
	}

	version, err := readRequiredInt(source, "version")
	if err != nil || version != PlaygroundHistoryVersion {
		return nil, invalidPlaygroundHistory("unsupported state version")
	}
	if err := validateOptionalString(source, "model", 200); err != nil {
		return nil, err
	}
	if err := validateObject(source, "parameters", true); err != nil {
		return nil, err
	}
	if err := validateOptionalNullableString(source, "activeConversationId", 160); err != nil {
		return nil, err
	}

	projects, err := readArray(source, "projects", playgroundHistoryMaxProjects)
	if err != nil {
		return nil, err
	}
	projectIDs := make(map[string]struct{}, len(projects))
	for _, rawProject := range projects {
		project, err := readObject(rawProject)
		if err != nil {
			return nil, invalidPlaygroundHistory("invalid project")
		}
		projectID, err := readRequiredString(project, "id", 160)
		if err != nil || strings.TrimSpace(projectID) == "" {
			return nil, invalidPlaygroundHistory("invalid project id")
		}
		if _, exists := projectIDs[projectID]; exists {
			return nil, invalidPlaygroundHistory("duplicate project id")
		}
		projectIDs[projectID] = struct{}{}
		if _, err := readRequiredString(project, "name", 80); err != nil {
			return nil, invalidPlaygroundHistory("invalid project name")
		}
	}

	conversations, err := readArray(source, "conversations", playgroundHistoryMaxConversations)
	if err != nil {
		return nil, err
	}
	conversationIDs := make(map[string]struct{}, len(conversations))
	for _, rawConversation := range conversations {
		conversation, err := readObject(rawConversation)
		if err != nil {
			return nil, invalidPlaygroundHistory("invalid conversation")
		}
		conversationID, err := readRequiredString(conversation, "id", 160)
		if err != nil || strings.TrimSpace(conversationID) == "" {
			return nil, invalidPlaygroundHistory("invalid conversation id")
		}
		if _, exists := conversationIDs[conversationID]; exists {
			return nil, invalidPlaygroundHistory("duplicate conversation id")
		}
		conversationIDs[conversationID] = struct{}{}
		if err := validateOptionalNullableString(conversation, "projectId", 160); err != nil {
			return nil, err
		}
		if err := validateOptionalString(conversation, "title", 80); err != nil {
			return nil, err
		}
		if err := validateOptionalString(conversation, "systemPrompt", 8000); err != nil {
			return nil, err
		}
		if err := validateOptionalString(conversation, "draftContent", 8000); err != nil {
			return nil, err
		}
		if rawMode, exists := conversation["mode"]; exists {
			var mode string
			if err := json.Unmarshal(rawMode, &mode); err != nil || (mode != "chat" && mode != "image") {
				return nil, invalidPlaygroundHistory("invalid conversation mode")
			}
		}

		messages, err := readArray(conversation, "messages", playgroundHistoryMaxMessages)
		if err != nil {
			return nil, err
		}
		for _, rawMessage := range messages {
			message, err := readObject(rawMessage)
			if err != nil {
				return nil, invalidPlaygroundHistory("invalid message")
			}
			if _, err := readRequiredString(message, "id", 160); err != nil {
				return nil, invalidPlaygroundHistory("invalid message id")
			}
			role, err := readRequiredString(message, "role", 20)
			if err != nil || (role != "system" && role != "user" && role != "assistant") {
				return nil, invalidPlaygroundHistory("invalid message role")
			}
			if err := validateOptionalString(message, "content", PlaygroundHistoryMaxMessageChars); err != nil {
				return nil, err
			}
			if err := validateOptionalString(message, "reasoningContent", PlaygroundHistoryMaxMessageChars); err != nil {
				return nil, err
			}
		}
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, state); err != nil {
		return nil, invalidPlaygroundHistory("state must be valid JSON")
	}
	return json.RawMessage(compact.Bytes()), nil
}

func readObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func readArray(source map[string]json.RawMessage, key string, maximum int) ([]json.RawMessage, error) {
	raw, exists := source[key]
	if !exists {
		return nil, invalidPlaygroundHistory(fmt.Sprintf("%s is required", key))
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || len(values) > maximum {
		return nil, invalidPlaygroundHistory(fmt.Sprintf("invalid %s", key))
	}
	return values, nil
}

func readRequiredInt(source map[string]json.RawMessage, key string) (int, error) {
	raw, exists := source[key]
	if !exists {
		return 0, fmt.Errorf("%s is required", key)
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	return value, nil
}

func readRequiredString(source map[string]json.RawMessage, key string, maximum int) (string, error) {
	raw, exists := source[key]
	if !exists {
		return "", fmt.Errorf("%s is required", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || len(value) > maximum {
		return "", fmt.Errorf("invalid %s", key)
	}
	return value, nil
}

func validateOptionalString(source map[string]json.RawMessage, key string, maximum int) error {
	raw, exists := source[key]
	if !exists {
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || len(value) > maximum {
		return invalidPlaygroundHistory(fmt.Sprintf("invalid %s", key))
	}
	return nil
}

func validateOptionalNullableString(source map[string]json.RawMessage, key string, maximum int) error {
	raw, exists := source[key]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	return validateOptionalString(source, key, maximum)
}

func validateObject(source map[string]json.RawMessage, key string, required bool) error {
	raw, exists := source[key]
	if !exists {
		if required {
			return invalidPlaygroundHistory(fmt.Sprintf("%s is required", key))
		}
		return nil
	}
	if _, err := readObject(raw); err != nil {
		return invalidPlaygroundHistory(fmt.Sprintf("invalid %s", key))
	}
	return nil
}

func invalidPlaygroundHistory(message string) error {
	return infraerrors.BadRequest("PLAYGROUND_HISTORY_INVALID", "Invalid Playground history: "+message)
}
