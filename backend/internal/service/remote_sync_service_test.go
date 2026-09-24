package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// memoryRemotePairingRepo mirrors the conditional updates of the SQL repository.
type memoryRemotePairingRepo struct {
	mu     sync.Mutex
	rows   map[int64]*RemotePairing
	nextID int64
}

func newMemoryRemotePairingRepo() *memoryRemotePairingRepo {
	return &memoryRemotePairingRepo{rows: map[int64]*RemotePairing{}}
}

func (r *memoryRemotePairingRepo) Create(_ context.Context, p *RemotePairing) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	p.ID = r.nextID
	p.CreatedAt = time.Now()
	copied := *p
	r.rows[p.ID] = &copied
	return nil
}

func (r *memoryRemotePairingRepo) GetForUser(_ context.Context, userID, id int64) (*RemotePairing, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.rows[id]
	if !ok || row.UserID != userID {
		return nil, ErrRemotePairingNotFound
	}
	copied := *row
	return &copied, nil
}

func (r *memoryRemotePairingRepo) FindPendingByCode(_ context.Context, userID int64, codeHash string, now time.Time) (*RemotePairing, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows {
		if row.UserID == userID && row.Status == RemotePairingPending && row.CodeHash == codeHash && row.CodeExpiresAt.After(now) {
			copied := *row
			return &copied, nil
		}
	}
	return nil, ErrRemotePairingNotFound
}

func (r *memoryRemotePairingRepo) RecordFailedClaim(_ context.Context, userID int64, maxAttempts int, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows {
		if row.UserID == userID && row.Status == RemotePairingPending {
			row.ClaimAttempts++
			if row.ClaimAttempts >= maxAttempts {
				row.Status = RemotePairingRevoked
				row.RevokedAt = &now
			}
		}
	}
	return nil
}

func (r *memoryRemotePairingRepo) RevokeOpenForDevice(_ context.Context, userID int64, deviceID string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows {
		if row.UserID == userID && row.DeviceID == deviceID && (row.Status == RemotePairingPending || row.Status == RemotePairingClaimed) {
			row.Status = RemotePairingRevoked
			row.RevokedAt = &now
		}
	}
	return nil
}

func (r *memoryRemotePairingRepo) Claim(_ context.Context, id int64, label, key, tokenHash string, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.rows[id]
	if !ok || row.Status != RemotePairingPending {
		return false, nil
	}
	row.Status, row.PhoneLabel, row.PhonePublicKey, row.TokenHash, row.ClaimedAt, row.CodeHash = RemotePairingClaimed, label, key, tokenHash, &now, ""
	return true, nil
}

func (r *memoryRemotePairingRepo) Confirm(_ context.Context, userID int64, deviceID string, id int64, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.rows[id]
	if !ok || row.UserID != userID || row.DeviceID != deviceID || row.Status != RemotePairingClaimed {
		return false, nil
	}
	row.Status, row.ConfirmedAt = RemotePairingActive, &now
	return true, nil
}

func (r *memoryRemotePairingRepo) Revoke(_ context.Context, userID, id int64, now time.Time) (*RemotePairing, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.rows[id]
	if !ok || row.UserID != userID {
		return nil, ErrRemotePairingNotFound
	}
	row.Status, row.RevokedAt, row.TokenHash = RemotePairingRevoked, &now, ""
	copied := *row
	return &copied, nil
}

func (r *memoryRemotePairingRepo) FindActiveByTokenHash(_ context.Context, tokenHash string) (*RemotePairing, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows {
		if row.TokenHash == tokenHash && row.TokenHash != "" {
			copied := *row
			return &copied, nil
		}
	}
	return nil, ErrRemotePairingNotFound
}

func (r *memoryRemotePairingRepo) list(match func(*RemotePairing) bool) []RemotePairing {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []RemotePairing
	for id := int64(1); id <= r.nextID; id++ {
		if row, ok := r.rows[id]; ok && match(row) {
			out = append(out, *row)
		}
	}
	return out
}

func (r *memoryRemotePairingRepo) ListClaimedForDevice(_ context.Context, userID int64, deviceID string) ([]RemotePairing, error) {
	return r.list(func(p *RemotePairing) bool {
		return p.UserID == userID && p.DeviceID == deviceID && p.Status == RemotePairingClaimed
	}), nil
}

func (r *memoryRemotePairingRepo) ListActiveForUser(_ context.Context, userID int64) ([]RemotePairing, error) {
	return r.list(func(p *RemotePairing) bool { return p.UserID == userID && p.Status == RemotePairingActive }), nil
}

func (r *memoryRemotePairingRepo) TouchLastUsed(_ context.Context, id int64, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if row, ok := r.rows[id]; ok {
		row.LastUsedAt = &now
	}
	return nil
}

var testPhoneKey = base64.StdEncoding.EncodeToString([]byte("spki-bytes-for-a-p256-key"))

const (
	testUser   int64 = 7
	testDevice       = "device-0001"
)

func newRemoteTestService() (*RemoteSyncService, *memoryRemotePairingRepo, *fakeAgentConn) {
	repo := newMemoryRemotePairingRepo()
	hub := NewRemoteHub()
	conn := newFakeAgentConn()
	hub.Attach(testUser, testDevice, conn)
	return NewRemoteSyncService(repo, hub), repo, conn
}

// The full happy path: code on the computer, claim on the phone, confirmation
// on the computer, then the phone's token opens that computer and no other.
func TestRemoteSync_PairingNeedsTheComputersConfirmation(t *testing.T) {
	svc, _, conn := newRemoteTestService()
	ctx := context.Background()

	start, err := svc.StartPairing(ctx, testUser, testDevice, "办公室电脑")
	require.NoError(t, err)
	require.Len(t, start.Code, 6)

	claim, err := svc.ClaimPairing(ctx, testUser, start.Code, "iPhone", testPhoneKey)
	require.NoError(t, err)
	require.NotEmpty(t, claim.Token)

	event := conn.next(t)
	require.Equal(t, "pair.request", event.Type)
	require.Contains(t, string(event.Body), testPhoneKey, "the computer must see the key it is asked to trust")

	_, err = svc.AuthenticatePhone(ctx, testUser, testDevice, claim.Token)
	require.ErrorIs(t, err, ErrRemotePairingNotActive, "claimed is not enough; the computer has not confirmed")

	require.NoError(t, svc.ConfirmPairing(ctx, testUser, testDevice, claim.PairingID))

	pairing, err := svc.AuthenticatePhone(ctx, testUser, testDevice, claim.Token)
	require.NoError(t, err)
	require.Equal(t, claim.PairingID, pairing.ID)

	_, err = svc.AuthenticatePhone(ctx, testUser, "device-0002", claim.Token)
	require.ErrorIs(t, err, ErrRemotePairingNotActive, "a token opens only the computer it was paired with")
	_, err = svc.AuthenticatePhone(ctx, 8, testDevice, claim.Token)
	require.ErrorIs(t, err, ErrRemotePairingNotActive, "and only for the account that paired it")
}

func TestRemoteSync_ACodeOnlyWorksForTheSameAccount(t *testing.T) {
	svc, _, _ := newRemoteTestService()
	start, err := svc.StartPairing(context.Background(), testUser, testDevice, "")
	require.NoError(t, err)

	_, err = svc.ClaimPairing(context.Background(), 8, start.Code, "phone", testPhoneKey)

	require.ErrorIs(t, err, ErrRemotePairingCodeInvalid)
}

func TestRemoteSync_AnExpiredCodeIsRejected(t *testing.T) {
	svc, _, _ := newRemoteTestService()
	start, err := svc.StartPairing(context.Background(), testUser, testDevice, "")
	require.NoError(t, err)

	svc.now = func() time.Time { return time.Now().Add(remoteCodeTTL + time.Second) }
	_, err = svc.ClaimPairing(context.Background(), testUser, start.Code, "phone", testPhoneKey)

	require.ErrorIs(t, err, ErrRemotePairingCodeInvalid)
}

// Six digits are a million guesses. Five wrong ones end every live code of the
// account, so guessing is not a strategy.
func TestRemoteSync_WrongGuessesBurnTheCode(t *testing.T) {
	svc, _, _ := newRemoteTestService()
	start, err := svc.StartPairing(context.Background(), testUser, testDevice, "")
	require.NoError(t, err)

	wrong := "000000"
	if start.Code == wrong {
		wrong = "111111"
	}
	for i := 0; i < remoteMaxClaimAttempts; i++ {
		_, err := svc.ClaimPairing(context.Background(), testUser, wrong, "phone", testPhoneKey)
		require.ErrorIs(t, err, ErrRemotePairingCodeInvalid)
	}

	_, err = svc.ClaimPairing(context.Background(), testUser, start.Code, "phone", testPhoneKey)
	require.ErrorIs(t, err, ErrRemotePairingCodeInvalid)
}

func TestRemoteSync_ACodeCannotBeClaimedTwice(t *testing.T) {
	svc, _, _ := newRemoteTestService()
	start, err := svc.StartPairing(context.Background(), testUser, testDevice, "")
	require.NoError(t, err)
	_, err = svc.ClaimPairing(context.Background(), testUser, start.Code, "first", testPhoneKey)
	require.NoError(t, err)

	_, err = svc.ClaimPairing(context.Background(), testUser, start.Code, "second", testPhoneKey)

	require.ErrorIs(t, err, ErrRemotePairingCodeInvalid)
}

func TestRemoteSync_ANewCodeWithdrawsTheOldOne(t *testing.T) {
	svc, _, _ := newRemoteTestService()
	first, err := svc.StartPairing(context.Background(), testUser, testDevice, "")
	require.NoError(t, err)
	_, err = svc.StartPairing(context.Background(), testUser, testDevice, "")
	require.NoError(t, err)

	_, err = svc.ClaimPairing(context.Background(), testUser, first.Code, "phone", testPhoneKey)

	require.ErrorIs(t, err, ErrRemotePairingCodeInvalid)
}

// A computer may confirm only claims made on its own code.
func TestRemoteSync_AnotherComputerCannotConfirm(t *testing.T) {
	svc, _, _ := newRemoteTestService()
	start, _ := svc.StartPairing(context.Background(), testUser, testDevice, "")
	claim, err := svc.ClaimPairing(context.Background(), testUser, start.Code, "phone", testPhoneKey)
	require.NoError(t, err)

	err = svc.ConfirmPairing(context.Background(), testUser, "device-0002", claim.PairingID)

	require.ErrorIs(t, err, ErrRemotePairingConflict)
}

func TestRemoteSync_RevokingEndsTheTokenAndTellsTheComputer(t *testing.T) {
	svc, _, conn := newRemoteTestService()
	ctx := context.Background()
	start, _ := svc.StartPairing(ctx, testUser, testDevice, "")
	claim, _ := svc.ClaimPairing(ctx, testUser, start.Code, "phone", testPhoneKey)
	conn.next(t)
	require.NoError(t, svc.ConfirmPairing(ctx, testUser, testDevice, claim.PairingID))

	require.NoError(t, svc.RevokePairing(ctx, testUser, claim.PairingID))

	require.Equal(t, "pair.revoked", conn.next(t).Type)
	_, err := svc.AuthenticatePhone(ctx, testUser, testDevice, claim.Token)
	require.ErrorIs(t, err, ErrRemotePairingNotActive)
}

// A phone that claimed while the computer was off is shown on its next connect.
func TestRemoteSync_ClaimsAreReplayedWhenTheComputerConnects(t *testing.T) {
	repo := newMemoryRemotePairingRepo()
	hub := NewRemoteHub()
	svc := NewRemoteSyncService(repo, hub)
	start, _ := svc.StartPairing(context.Background(), testUser, testDevice, "")
	_, err := svc.ClaimPairing(context.Background(), testUser, start.Code, "phone", testPhoneKey)
	require.NoError(t, err)

	conn := newFakeAgentConn()
	hub.Attach(testUser, testDevice, conn)
	svc.AgentConnected(context.Background(), testUser, testDevice)

	require.Equal(t, "pair.request", conn.next(t).Type)
}

func TestRemoteSync_OnlyKnownCommandsAreRelayed(t *testing.T) {
	svc, _, _ := newRemoteTestService()
	pairing := &RemotePairing{ID: 1, UserID: testUser, DeviceID: testDevice}

	for _, body := range []string{`{"type":"fs.read","path":"C:/x"}`, `not json`, `{}`} {
		_, err := svc.Forward(context.Background(), pairing, json.RawMessage(body))
		require.ErrorIs(t, err, ErrRemoteCommandRefused, body)
	}

	// The phone's 电脑自检 after a failed turn: let through (and then refused as offline here).
	_, err := svc.Forward(context.Background(), pairing, json.RawMessage(`{"type":"desktop.check","thread_id":"t"}`))
	require.NotErrorIs(t, err, ErrRemoteCommandRefused)
}

func TestRemoteSync_ABadPublicKeyIsRefusedBeforeTheCodeIsSpent(t *testing.T) {
	svc, _, _ := newRemoteTestService()
	start, _ := svc.StartPairing(context.Background(), testUser, testDevice, "")

	_, err := svc.ClaimPairing(context.Background(), testUser, start.Code, "phone", "not base64!")
	require.ErrorIs(t, err, ErrRemoteBadPublicKey)

	_, err = svc.ClaimPairing(context.Background(), testUser, start.Code, "phone", testPhoneKey)
	require.NoError(t, err)
}

func TestRemoteSync_DeviceIDsAreValidated(t *testing.T) {
	svc, _, _ := newRemoteTestService()
	for _, id := range []string{"", "short", "has space in it", "../../etc/passwd"} {
		_, err := svc.StartPairing(context.Background(), testUser, id, "")
		require.ErrorIs(t, err, ErrRemoteBadDevice, id)
	}
}
