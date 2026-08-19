package dashboard

import (
	"context"
	"testing"

	"transithub/backend/internal/modules/upstream"
)

func TestLoginWithNewAPIAdminKeyPersistsKeySession(t *testing.T) {
	store := newFakeSessionStore()
	accounts := &fakeAdminAccounts{current: map[string]string{"transit-user": "account-1"}}
	expected := upstream.Session{
		Platform: upstream.PlatformNewAPI, BaseURL: "https://new-api.example.com",
		AccessToken: "root-key", TokenType: "Bearer", UserID: "99",
	}
	platform := &fakePlatformClient{adminKeyResult: &expected}
	service := NewService(store, platform)
	service.SetAdminAccountService(accounts)

	status, err := service.Login(context.Background(), "transit-user", LoginRequest{
		Platform: PlatformNewAPI, SiteURL: "https://new-api.example.com",
		AuthMethod: AuthMethodAdminKey, AdminKey: "root-key", UserID: "99",
	})
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if !status.Authenticated || status.AuthMethod != AuthMethodAdminKey {
		t.Fatalf("unexpected status: %+v", status)
	}
	if platform.capturedPlatform != upstream.PlatformNewAPI || platform.capturedAdminKey != "root-key" || platform.capturedUserID != "99" {
		t.Fatalf("unexpected key login args platform=%s key=%q userID=%q", platform.capturedPlatform, platform.capturedAdminKey, platform.capturedUserID)
	}
	saved, _ := store.Get(context.Background(), "transit-user", "account-1")
	if saved == nil || saved.Session.AccessToken != "root-key" || saved.Session.UserID != "99" {
		t.Fatalf("unexpected persisted session: %+v", saved)
	}
	if accounts.upsertInput.AuthMethod != AuthMethodAdminKey {
		t.Fatalf("expected admin_key workspace auth method, got %q", accounts.upsertInput.AuthMethod)
	}
}

func TestLoginWithSub2APIAdminKeyPersistsXAPIKeyCredential(t *testing.T) {
	store := newFakeSessionStore()
	accounts := &fakeAdminAccounts{current: map[string]string{"transit-user": "account-1"}}
	expected := upstream.Session{
		Platform: upstream.PlatformSub2API, BaseURL: "https://sub2api.example.com", AdminAPIKey: "admin-key",
	}
	platform := &fakePlatformClient{adminKeyResult: &expected}
	service := NewService(store, platform)
	service.SetAdminAccountService(accounts)

	_, err := service.Login(context.Background(), "transit-user", LoginRequest{
		Platform: PlatformSub2API, SiteURL: "https://sub2api.example.com",
		AuthMethod: AuthMethodAdminKey, AdminKey: "admin-key",
	})
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	saved, _ := store.Get(context.Background(), "transit-user", "account-1")
	if saved == nil || saved.Session.AdminAPIKey != "admin-key" || saved.Session.AccessToken != "" {
		t.Fatalf("unexpected persisted session: %+v", saved)
	}
}
