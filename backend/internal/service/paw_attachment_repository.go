package service

import (
	"context"
	"errors"
	"sync"
)

var errPawAttachmentRepoMissing = errors.New("paw attachment repository is missing")

type PawAttachmentMemoryRepository struct {
	mu          sync.RWMutex
	attachments map[string]*PawAttachment
}

func NewPawAttachmentMemoryRepository() *PawAttachmentMemoryRepository {
	return &PawAttachmentMemoryRepository{
		attachments: make(map[string]*PawAttachment),
	}
}

func (r *PawAttachmentMemoryRepository) Save(_ context.Context, attachment *PawAttachment) error {
	if r == nil || attachment == nil || attachment.ID == "" {
		return errPawAttachmentRepoMissing
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.attachments == nil {
		r.attachments = make(map[string]*PawAttachment)
	}
	copyAttachment := *attachment
	if len(attachment.Data) > 0 {
		copyAttachment.Data = append([]byte(nil), attachment.Data...)
	}
	r.attachments[attachment.ID] = &copyAttachment
	return nil
}

func (r *PawAttachmentMemoryRepository) Get(_ context.Context, attachmentID string) (*PawAttachment, error) {
	if r == nil {
		return nil, errPawAttachmentRepoMissing
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	attachment, ok := r.attachments[attachmentID]
	if !ok || attachment == nil {
		return nil, nil
	}
	copyAttachment := *attachment
	if len(attachment.Data) > 0 {
		copyAttachment.Data = append([]byte(nil), attachment.Data...)
	}
	return &copyAttachment, nil
}
