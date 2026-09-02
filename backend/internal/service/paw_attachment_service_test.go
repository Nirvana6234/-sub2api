package service

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestPawAttachmentServiceAcceptsSupportedMIMETypes(t *testing.T) {
	svc := NewPawAttachmentService(&pawAttachmentRepoStub{attachments: map[string]*PawAttachment{}}, 30*time.Minute, 8<<20)
	fixed := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixed }

	cases := []struct {
		name     string
		filename string
		mimeType string
		data     []byte
	}{
		{name: "png", filename: "image.png", mimeType: "image/png", data: []byte{0x89, 'P', 'N', 'G'}},
		{name: "pdf", filename: "doc.pdf", mimeType: "application/pdf", data: []byte("%PDF-1.7")},
		{name: "docx", filename: "doc.docx", mimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", data: []byte("PK")},
		{name: "txt", filename: "notes.txt", mimeType: "text/plain", data: []byte("hello")},
		{name: "md", filename: "notes.md", mimeType: "text/markdown", data: []byte("# hi")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			att, err := svc.Upload(context.Background(), 42, tc.filename, tc.mimeType, tc.data)
			require.NoError(t, err)
			require.Equal(t, int64(42), att.UserID)
			require.Equal(t, tc.filename, att.Filename)
			require.Equal(t, tc.mimeType, att.MIMEType)
			require.Equal(t, int64(len(tc.data)), att.Size)
			require.Equal(t, fixed.Add(30*time.Minute), att.ExpiresAt)
		})
	}
}

func TestPawAttachmentServiceRejectsUnsupportedMIMEType(t *testing.T) {
	svc := NewPawAttachmentService(&pawAttachmentRepoStub{attachments: map[string]*PawAttachment{}}, time.Hour, 8<<20)

	_, err := svc.Upload(context.Background(), 42, "archive.zip", "application/zip", []byte("zip"))

	require.Error(t, err)
	require.Equal(t, "ATTACHMENT_INVALID", infraerrors.Reason(err))
}

func TestPawAttachmentServiceRejectsOversizedAttachment(t *testing.T) {
	svc := NewPawAttachmentService(&pawAttachmentRepoStub{attachments: map[string]*PawAttachment{}}, time.Hour, 4)

	_, err := svc.Upload(context.Background(), 42, "notes.txt", "text/plain", bytes.Repeat([]byte("x"), 5))

	require.Error(t, err)
	require.Equal(t, "ATTACHMENT_INVALID", infraerrors.Reason(err))
}

func TestPawAttachmentServiceRejectsWrongOwner(t *testing.T) {
	repo := &pawAttachmentRepoStub{attachments: map[string]*PawAttachment{
		"att-1": {
			ID:        "att-1",
			UserID:    7,
			Filename:  "notes.txt",
			MIMEType:  "text/plain",
			Size:      5,
			ExpiresAt: time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC),
			Data:      []byte("hello"),
		},
	}}
	svc := NewPawAttachmentService(repo, time.Hour, 8<<20)

	_, err := svc.Get(context.Background(), 42, "att-1")

	require.Error(t, err)
	require.Equal(t, "ATTACHMENT_INVALID", infraerrors.Reason(err))
}

func TestPawAttachmentServiceRejectsExpiredAttachment(t *testing.T) {
	fixed := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	repo := &pawAttachmentRepoStub{attachments: map[string]*PawAttachment{
		"att-1": {
			ID:        "att-1",
			UserID:    42,
			Filename:  "notes.txt",
			MIMEType:  "text/plain",
			Size:      5,
			ExpiresAt: fixed.Add(-time.Minute),
			Data:      []byte("hello"),
		},
	}}
	svc := NewPawAttachmentService(repo, time.Hour, 8<<20)
	svc.now = func() time.Time { return fixed }

	_, err := svc.Get(context.Background(), 42, "att-1")

	require.Error(t, err)
	require.Equal(t, "ATTACHMENT_INVALID", infraerrors.Reason(err))
}

type pawAttachmentRepoStub struct {
	attachments map[string]*PawAttachment
	saved       *PawAttachment
}

func (s *pawAttachmentRepoStub) Save(_ context.Context, attachment *PawAttachment) error {
	if s.attachments == nil {
		s.attachments = map[string]*PawAttachment{}
	}
	copyAttachment := *attachment
	s.saved = &copyAttachment
	s.attachments[attachment.ID] = &copyAttachment
	return nil
}

func (s *pawAttachmentRepoStub) Get(_ context.Context, attachmentID string) (*PawAttachment, error) {
	if att, ok := s.attachments[attachmentID]; ok {
		copyAttachment := *att
		return &copyAttachment, nil
	}
	return nil, errors.New("attachment not found")
}
