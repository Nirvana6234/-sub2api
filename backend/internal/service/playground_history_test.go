package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type playgroundHistoryRepositoryStub struct {
	state      json.RawMessage
	savedUser  int64
	savedState json.RawMessage
	savedAt    time.Time
	getErr     error
	saveErr    error
}

func (r *playgroundHistoryRepositoryStub) Get(_ context.Context, _ int64) (json.RawMessage, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.state, nil
}

func (r *playgroundHistoryRepositoryStub) Save(_ context.Context, userID int64, state json.RawMessage, updatedAt time.Time) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.savedUser = userID
	r.savedState = append(json.RawMessage(nil), state...)
	r.savedAt = updatedAt
	return nil
}

func validPlaygroundHistoryState() json.RawMessage {
	return json.RawMessage(`{
        "version": 2,
        "model": "gpt-4.1",
        "parameters": {"temperature": 0.7, "stream": true},
        "activeConversationId": "conversation-1",
        "projects": [{"id": "project-1", "name": "Research"}],
        "conversations": [{
            "id": "conversation-1",
            "projectId": "project-1",
            "title": "Evaluate options",
            "systemPrompt": "Be concise.",
            "draftContent": "",
            "messages": [{"id": "message-1", "role": "user", "content": "Compare the options."}]
        }]
    }`)
}

func TestPlaygroundHistoryServiceSaveScopesAndCompactsState(t *testing.T) {
	repo := &playgroundHistoryRepositoryStub{}
	svc := NewPlaygroundHistoryService(repo)

	require.NoError(t, svc.Save(context.Background(), 7, validPlaygroundHistoryState()))
	require.Equal(t, int64(7), repo.savedUser)
	require.NotEmpty(t, repo.savedAt)
	require.JSONEq(t, string(validPlaygroundHistoryState()), string(repo.savedState))
	require.NotContains(t, string(repo.savedState), "\n")
}

func TestPlaygroundHistoryServiceRejectsInvalidStateBeforePersistence(t *testing.T) {
	repo := &playgroundHistoryRepositoryStub{}
	svc := NewPlaygroundHistoryService(repo)

	invalidRole := json.RawMessage(strings.Replace(string(validPlaygroundHistoryState()), `"role": "user"`, `"role": "tool"`, 1))
	err := svc.Save(context.Background(), 7, invalidRole)
	require.Error(t, err)
	require.Empty(t, repo.savedState)

	invalidVersion := json.RawMessage(strings.Replace(string(validPlaygroundHistoryState()), `"version": 2`, `"version": 3`, 1))
	err = svc.Save(context.Background(), 7, invalidVersion)
	require.Error(t, err)
	require.Empty(t, repo.savedState)

	invalidMode := json.RawMessage(strings.Replace(string(validPlaygroundHistoryState()), `"title": "Evaluate options",`, `"title": "Evaluate options", "mode": "video",`, 1))
	err = svc.Save(context.Background(), 7, invalidMode)
	require.Error(t, err)
	require.Empty(t, repo.savedState)
}

func TestPlaygroundHistoryServiceGetKeepsUserScope(t *testing.T) {
	expected := validPlaygroundHistoryState()
	repo := &playgroundHistoryRepositoryStub{state: expected}
	svc := NewPlaygroundHistoryService(repo)

	actual, err := svc.Get(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, expected, actual)

	_, err = svc.Get(context.Background(), 0)
	require.Error(t, err)
}
