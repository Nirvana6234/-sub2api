package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// RemotePairingStatus is the life of a phone ↔ computer pairing.
//
// pending: the computer showed a code. claimed: a phone of the same account
// entered it. active: the user confirmed it on the computer. revoked: ended by
// either side, or by too many wrong codes.
type RemotePairingStatus string

const (
	RemotePairingPending RemotePairingStatus = "pending"
	RemotePairingClaimed RemotePairingStatus = "claimed"
	RemotePairingActive  RemotePairingStatus = "active"
	RemotePairingRevoked RemotePairingStatus = "revoked"
)

type RemotePairing struct {
	ID             int64
	UserID         int64
	DeviceID       string
	DeviceName     string
	Status         RemotePairingStatus
	CodeHash       string
	CodeExpiresAt  *time.Time
	ClaimAttempts  int
	PhoneLabel     string
	PhonePublicKey string
	TokenHash      string
	CreatedAt      time.Time
	ClaimedAt      *time.Time
	ConfirmedAt    *time.Time
	RevokedAt      *time.Time
	LastUsedAt     *time.Time
}

// RemotePairingRepository stores pairings. Every state change is conditional
// on the current state, so two racing requests cannot both win.
type RemotePairingRepository interface {
	Create(ctx context.Context, pairing *RemotePairing) error
	GetForUser(ctx context.Context, userID, id int64) (*RemotePairing, error)
	FindPendingByCode(ctx context.Context, userID int64, codeHash string, now time.Time) (*RemotePairing, error)
	// RecordFailedClaim counts a wrong code against every live code of the user
	// and revokes those that reached maxAttempts.
	RecordFailedClaim(ctx context.Context, userID int64, maxAttempts int, now time.Time) error
	RevokeOpenForDevice(ctx context.Context, userID int64, deviceID string, now time.Time) error
	Claim(ctx context.Context, id int64, phoneLabel, publicKey, tokenHash string, now time.Time) (bool, error)
	Confirm(ctx context.Context, userID int64, deviceID string, id int64, now time.Time) (bool, error)
	Revoke(ctx context.Context, userID, id int64, now time.Time) (*RemotePairing, error)
	FindActiveByTokenHash(ctx context.Context, tokenHash string) (*RemotePairing, error)
	ListClaimedForDevice(ctx context.Context, userID int64, deviceID string) ([]RemotePairing, error)
	ListActiveForUser(ctx context.Context, userID int64) ([]RemotePairing, error)
	TouchLastUsed(ctx context.Context, id int64, now time.Time) error
}

var (
	ErrRemotePairingNotFound    = infraerrors.NotFound("REMOTE_PAIRING_NOT_FOUND", "pairing not found")
	ErrRemotePairingCodeInvalid = infraerrors.BadRequest("REMOTE_PAIRING_CODE_INVALID", "the pairing code is wrong or has expired")
	ErrRemotePairingNotActive   = infraerrors.Forbidden("REMOTE_PAIRING_NOT_ACTIVE", "this phone is not paired with that computer")
	ErrRemotePairingConflict    = infraerrors.Conflict("REMOTE_PAIRING_CONFLICT", "the pairing changed state; try again")
	ErrRemoteBadDevice          = infraerrors.BadRequest("REMOTE_BAD_DEVICE", "device id must be 8-64 letters, digits, '-' or '_'")
	ErrRemoteBadPublicKey       = infraerrors.BadRequest("REMOTE_BAD_PUBLIC_KEY", "public key must be a base64 SPKI of at most 1 KB")
	ErrRemoteCommandRefused     = infraerrors.BadRequest("REMOTE_COMMAND_REFUSED", "command type is not allowed")
)

const (
	remoteCodeTTL            = 5 * time.Minute
	remoteMaxClaimAttempts   = 5
	remoteMaxLabelRunes      = 100
	remoteMaxPublicKeyBytes  = 1024
	remoteMaxCommandBytes    = 64 * 1024
	remoteTokenBytes         = 32
	remoteTouchMinimumPeriod = time.Minute
)

var remoteDeviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// ValidateRemoteDeviceID checks the id an assistant generated for its computer.
func ValidateRemoteDeviceID(deviceID string) error {
	if !remoteDeviceIDPattern.MatchString(deviceID) {
		return ErrRemoteBadDevice
	}
	return nil
}

// remoteCommandTypes are what a phone may ask of a computer. The assistant
// enforces its own list; this is the second line, so a stray client cannot
// even reach it with anything else.
var remoteCommandTypes = map[string]bool{
	"sessions.list":   true,
	"session.open":    true,
	"session.history": true,
	"session.detail":  true,
	"message.send":    true,
	"thread.navigate": true,
}

// RemoteSyncService pairs phones with computers and relays between them.
type RemoteSyncService struct {
	repo RemotePairingRepository
	hub  *RemoteHub
	now  func() time.Time
}

func NewRemoteSyncService(repo RemotePairingRepository, hub *RemoteHub) *RemoteSyncService {
	return &RemoteSyncService{repo: repo, hub: hub, now: time.Now}
}

func (s *RemoteSyncService) Hub() *RemoteHub { return s.hub }

type RemotePairingStart struct {
	PairingID int64
	Code      string
	ExpiresAt time.Time
}

// StartPairing issues a code for the computer to show. Any earlier code or
// unconfirmed claim for the same computer is withdrawn: only the latest code
// on screen should work.
func (s *RemoteSyncService) StartPairing(ctx context.Context, userID int64, deviceID, deviceName string) (*RemotePairingStart, error) {
	if err := ValidateRemoteDeviceID(deviceID); err != nil {
		return nil, err
	}
	now := s.now()
	if err := s.repo.RevokeOpenForDevice(ctx, userID, deviceID, now); err != nil {
		return nil, err
	}

	code, err := remoteRandomDigits(6)
	if err != nil {
		return nil, err
	}
	expires := now.Add(remoteCodeTTL)
	pairing := &RemotePairing{
		UserID:        userID,
		DeviceID:      deviceID,
		DeviceName:    remoteTruncateRunes(strings.TrimSpace(deviceName), remoteMaxLabelRunes),
		Status:        RemotePairingPending,
		CodeHash:      remoteHashSecret(code),
		CodeExpiresAt: &expires,
	}
	if err := s.repo.Create(ctx, pairing); err != nil {
		return nil, err
	}
	return &RemotePairingStart{PairingID: pairing.ID, Code: code, ExpiresAt: expires}, nil
}

type RemotePairingClaim struct {
	PairingID  int64
	DeviceID   string
	DeviceName string
	// Token is the phone's bearer secret for this pairing; only its hash is stored.
	Token string
}

// ClaimPairing lets a phone signed in to the same account take a code. The
// pairing is not usable until the user confirms it on the computer, which is
// told now if it is online and again whenever it reconnects.
func (s *RemoteSyncService) ClaimPairing(ctx context.Context, userID int64, code, phoneLabel, publicKey string) (*RemotePairingClaim, error) {
	if err := remoteValidatePublicKey(publicKey); err != nil {
		return nil, err
	}
	now := s.now()
	pairing, err := s.repo.FindPendingByCode(ctx, userID, remoteHashSecret(strings.TrimSpace(code)), now)
	if errors.Is(err, ErrRemotePairingNotFound) {
		if recordErr := s.repo.RecordFailedClaim(ctx, userID, remoteMaxClaimAttempts, now); recordErr != nil {
			return nil, recordErr
		}
		return nil, ErrRemotePairingCodeInvalid
	}
	if err != nil {
		return nil, err
	}

	token, err := remoteRandomToken()
	if err != nil {
		return nil, err
	}
	label := remoteTruncateRunes(strings.TrimSpace(phoneLabel), remoteMaxLabelRunes)
	ok, err := s.repo.Claim(ctx, pairing.ID, label, publicKey, remoteHashSecret(token), now)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrRemotePairingCodeInvalid
	}

	pairing.PhoneLabel = label
	pairing.PhonePublicKey = publicKey
	s.notifyPairRequest(pairing)
	return &RemotePairingClaim{PairingID: pairing.ID, DeviceID: pairing.DeviceID, DeviceName: pairing.DeviceName, Token: token}, nil
}

// ConfirmPairing is sent by the computer after the user approved the phone there.
func (s *RemoteSyncService) ConfirmPairing(ctx context.Context, userID int64, deviceID string, pairingID int64) error {
	ok, err := s.repo.Confirm(ctx, userID, deviceID, pairingID, s.now())
	if err != nil {
		return err
	}
	if !ok {
		return ErrRemotePairingConflict
	}
	return nil
}

// RevokePairing ends a pairing from either side; the computer is told.
func (s *RemoteSyncService) RevokePairing(ctx context.Context, userID, pairingID int64) error {
	pairing, err := s.repo.Revoke(ctx, userID, pairingID, s.now())
	if err != nil {
		return err
	}
	s.hub.Notify(userID, pairing.DeviceID, "pair.revoked", map[string]any{"pairing_id": pairing.ID})
	return nil
}

// RejectPairing is the computer declining a claim; it may only reject its own.
func (s *RemoteSyncService) RejectPairing(ctx context.Context, userID int64, deviceID string, pairingID int64) error {
	pairing, err := s.repo.GetForUser(ctx, userID, pairingID)
	if err != nil {
		return err
	}
	if pairing.DeviceID != deviceID {
		return ErrRemotePairingNotFound
	}
	_, err = s.repo.Revoke(ctx, userID, pairingID, s.now())
	return err
}

func (s *RemoteSyncService) PairingStatus(ctx context.Context, userID, pairingID int64) (RemotePairingStatus, error) {
	pairing, err := s.repo.GetForUser(ctx, userID, pairingID)
	if err != nil {
		return "", err
	}
	return pairing.Status, nil
}

// AuthenticatePhone resolves the phone's pairing token for one computer.
// Signed-in is not enough: the token must belong to an active pairing of this
// user with this very computer.
func (s *RemoteSyncService) AuthenticatePhone(ctx context.Context, userID int64, deviceID, token string) (*RemotePairing, error) {
	if token == "" {
		return nil, ErrRemotePairingNotActive
	}
	pairing, err := s.repo.FindActiveByTokenHash(ctx, remoteHashSecret(token))
	if errors.Is(err, ErrRemotePairingNotFound) {
		return nil, ErrRemotePairingNotActive
	}
	if err != nil {
		return nil, err
	}
	if pairing.UserID != userID || pairing.DeviceID != deviceID || pairing.Status != RemotePairingActive {
		return nil, ErrRemotePairingNotActive
	}

	now := s.now()
	if pairing.LastUsedAt == nil || now.Sub(*pairing.LastUsedAt) > remoteTouchMinimumPeriod {
		_ = s.repo.TouchLastUsed(ctx, pairing.ID, now)
	}
	return pairing, nil
}

type RemoteDevice struct {
	DeviceID   string
	DeviceName string
	Online     bool
	Status     json.RawMessage
	Pairings   []RemotePairing
}

// ListDevices lists the user's computers that have an active pairing.
func (s *RemoteSyncService) ListDevices(ctx context.Context, userID int64) ([]RemoteDevice, error) {
	pairings, err := s.repo.ListActiveForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	var devices []RemoteDevice
	index := map[string]int{}
	for _, pairing := range pairings {
		i, ok := index[pairing.DeviceID]
		if !ok {
			i = len(devices)
			index[pairing.DeviceID] = i
			devices = append(devices, RemoteDevice{
				DeviceID:   pairing.DeviceID,
				DeviceName: pairing.DeviceName,
				Online:     s.hub.Online(userID, pairing.DeviceID),
				Status:     s.hub.Hello(userID, pairing.DeviceID),
			})
		}
		devices[i].Pairings = append(devices[i].Pairings, pairing)
	}
	return devices, nil
}

// Forward relays one phone command. Only the envelope is checked here.
func (s *RemoteSyncService) Forward(ctx context.Context, pairing *RemotePairing, body json.RawMessage) (json.RawMessage, error) {
	if err := remoteCheckCommand(body); err != nil {
		return nil, err
	}
	return s.hub.Request(ctx, pairing.UserID, pairing.DeviceID, pairing.ID, body)
}

// Subscribe opens a stream of one conversation's changes for a phone.
func (s *RemoteSyncService) Subscribe(pairing *RemotePairing, threadID, cursor string) (<-chan json.RawMessage, func(), error) {
	body, err := json.Marshal(map[string]string{"type": "session.subscribe", "thread_id": threadID, "cursor": cursor})
	if err != nil {
		return nil, nil, err
	}
	return s.hub.Subscribe(pairing.UserID, pairing.DeviceID, pairing.ID, body)
}

// AgentConnected replays unconfirmed claims to a computer that just connected,
// so a phone that claimed a code while it was offline is not left waiting.
func (s *RemoteSyncService) AgentConnected(ctx context.Context, userID int64, deviceID string) {
	claimed, err := s.repo.ListClaimedForDevice(ctx, userID, deviceID)
	if err != nil {
		return
	}
	for i := range claimed {
		s.notifyPairRequest(&claimed[i])
	}
}

// HandleAgentFrame acts on what the computer sends besides answers.
func (s *RemoteSyncService) HandleAgentFrame(ctx context.Context, userID int64, deviceID string, frame *RemoteAgentFrame) error {
	switch frame.Kind {
	case "pair.confirm":
		return s.ConfirmPairing(ctx, userID, deviceID, frame.PairingID)
	case "pair.reject":
		return s.RejectPairing(ctx, userID, deviceID, frame.PairingID)
	default:
		return infraerrors.BadRequest("REMOTE_UNKNOWN_FRAME", fmt.Sprintf("unknown frame kind %q", frame.Kind))
	}
}

func (s *RemoteSyncService) notifyPairRequest(pairing *RemotePairing) {
	s.hub.Notify(pairing.UserID, pairing.DeviceID, "pair.request", map[string]any{
		"pairing_id":  pairing.ID,
		"phone_label": pairing.PhoneLabel,
		"public_key":  pairing.PhonePublicKey,
	})
}

func remoteCheckCommand(body json.RawMessage) error {
	if len(body) == 0 || len(body) > remoteMaxCommandBytes {
		return ErrRemoteCommandRefused
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || !remoteCommandTypes[envelope.Type] {
		return ErrRemoteCommandRefused
	}
	return nil
}

func remoteValidatePublicKey(key string) error {
	if key == "" || len(key) > remoteMaxPublicKeyBytes*2 {
		return ErrRemoteBadPublicKey
	}
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) == 0 || len(raw) > remoteMaxPublicKeyBytes {
		return ErrRemoteBadPublicKey
	}
	return nil
}

func remoteHashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func remoteRandomDigits(n int) (string, error) {
	var b strings.Builder
	for i := 0; i < n; i++ {
		d, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		b.WriteByte(byte('0' + d.Int64()))
	}
	return b.String(), nil
}

func remoteRandomToken() (string, error) {
	buf := make([]byte, remoteTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func remoteTruncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}
