package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

const (
	defaultPawAttachmentTTL      = 24 * time.Hour
	defaultPawAttachmentMaxBytes  = 20 << 20
	pawAttachmentInlineImageLimit = 5 << 20
)

var (
	ErrPawAttachmentNotFound = infraerrors.BadRequest("ATTACHMENT_INVALID", "attachment not found")
	errPawAttachmentInvalid  = infraerrors.BadRequest("ATTACHMENT_INVALID", "attachment is invalid")
	errPawAttachmentMissing  = infraerrors.ServiceUnavailable("CONFIG_UNAVAILABLE", "Paw attachments are unavailable")
)

type PawAttachment struct {
	ID        string    `json:"id"`
	UserID    int64     `json:"user_id"`
	Filename  string    `json:"filename"`
	MIMEType  string    `json:"mime_type"`
	Size      int64     `json:"size"`
	ExpiresAt time.Time `json:"expires_at"`
	Data      []byte    `json:"data,omitempty"`
}

type PawAttachmentRepository interface {
	Save(ctx context.Context, attachment *PawAttachment) error
	Get(ctx context.Context, attachmentID string) (*PawAttachment, error)
}

type PawAttachmentService struct {
	repo     PawAttachmentRepository
	ttl      time.Duration
	maxBytes int64
	now      func() time.Time
}

func NewPawAttachmentService(repo PawAttachmentRepository, ttl time.Duration, maxBytes int64) *PawAttachmentService {
	if ttl <= 0 {
		ttl = defaultPawAttachmentTTL
	}
	if maxBytes <= 0 {
		maxBytes = defaultPawAttachmentMaxBytes
	}
	return &PawAttachmentService{
		repo:     repo,
		ttl:      ttl,
		maxBytes: maxBytes,
		now:      time.Now,
	}
}

func (s *PawAttachmentService) Upload(ctx context.Context, userID int64, filename, contentType string, data []byte) (*PawAttachment, error) {
	if s == nil || s.repo == nil {
		return nil, errPawAttachmentMissing
	}
	if userID <= 0 {
		return nil, errPawAttachmentInvalid
	}
	if len(data) == 0 || int64(len(data)) > s.maxBytes {
		return nil, errPawAttachmentInvalid
	}
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return nil, errPawAttachmentInvalid
	}
	mimeType, err := normalizePawAttachmentMIMEType(contentType, data)
	if err != nil {
		return nil, errPawAttachmentInvalid
	}

	now := s.now()
	attachment := &PawAttachment{
		ID:        uuid.NewString(),
		UserID:    userID,
		Filename:  filename,
		MIMEType:  mimeType,
		Size:      int64(len(data)),
		ExpiresAt: now.Add(s.ttl),
		Data:      append([]byte(nil), data...),
	}
	if err := s.repo.Save(ctx, attachment); err != nil {
		return nil, err
	}
	return attachment, nil
}

func (s *PawAttachmentService) Get(ctx context.Context, userID int64, attachmentID string) (*PawAttachment, error) {
	if s == nil || s.repo == nil {
		return nil, errPawAttachmentMissing
	}
	attachmentID = strings.TrimSpace(attachmentID)
	if userID <= 0 || attachmentID == "" {
		return nil, errPawAttachmentInvalid
	}
	attachment, err := s.repo.Get(ctx, attachmentID)
	if err != nil {
		return nil, errPawAttachmentInvalid
	}
	if attachment == nil {
		return nil, ErrPawAttachmentNotFound
	}
	if attachment.UserID != userID {
		return nil, errPawAttachmentInvalid
	}
	if !attachment.ExpiresAt.IsZero() && !s.now().Before(attachment.ExpiresAt) {
		return nil, errPawAttachmentInvalid
	}
	return attachment, nil
}

func (s *PawAttachmentService) BuildChatContentParts(ctx context.Context, userID int64, refs []PawAttachmentReference) ([]apicompat.ChatContentPart, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	parts := make([]apicompat.ChatContentPart, 0, len(refs))
	for _, ref := range refs {
		attachment, err := s.Get(ctx, userID, ref.ID)
		if err != nil {
			return nil, err
		}
		part, err := pawAttachmentContentPart(attachment)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func pawAttachmentContentPart(attachment *PawAttachment) (apicompat.ChatContentPart, error) {
	if attachment == nil {
		return apicompat.ChatContentPart{}, errPawAttachmentInvalid
	}
	mimeType := strings.TrimSpace(attachment.MIMEType)
	dataURI := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(attachment.Data)
	if isPawAttachmentImageMIMEType(mimeType) {
		return apicompat.ChatContentPart{
			Type: "image_url",
			ImageURL: &apicompat.ChatImageURL{
				URL: dataURI,
			},
		}, nil
	}
	return apicompat.ChatContentPart{
		Type: "file",
		File: &apicompat.ChatFile{
			Filename: attachment.Filename,
			FileData: dataURI,
		},
	}, nil
}

func normalizePawAttachmentMIMEType(contentType string, data []byte) (string, error) {
	mimeType := strings.TrimSpace(contentType)
	if mimeType != "" {
		if parsed, _, err := mime.ParseMediaType(mimeType); err == nil && parsed != "" {
			mimeType = parsed
		}
	}
	if mimeType == "" && len(data) > 0 {
		mimeType = strings.TrimSpace(httpDetectContentType(data))
	}
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch mimeType {
	case "image/png", "image/jpeg", "image/webp", "image/gif",
		"application/pdf",
		"application/msword",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"text/plain",
		"text/markdown",
		"text/x-markdown",
		"application/markdown":
		return mimeType, nil
	default:
		return "", fmt.Errorf("unsupported attachment type %q", mimeType)
	}
}

func isPawAttachmentImageMIMEType(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

func httpDetectContentType(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if len(data) > pawAttachmentInlineImageLimit {
		data = data[:pawAttachmentInlineImageLimit]
	}
	return http.DetectContentType(data)
}
