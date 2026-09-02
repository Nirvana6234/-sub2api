package service

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type PawImageGenerationRequest struct {
	GroupID int64
	ModelID string
	Prompt  string
	Size    string
	N       int
	Stream  bool
}

type PawImageEditAttachment struct {
	FieldName string
	Filename  string
	MIMEType  string
	Data      []byte
}

type PawImageEditRequest struct {
	GroupID int64
	ModelID string
	Prompt  string
	Size    string
	N       int
	Images  []PawImageEditAttachment
	Mask    *PawImageEditAttachment
}

type PawImageResolution struct {
	APIKey *APIKey
	Group  *Group
	Model  PawModel
}

type PawImageService struct {
	config     *PawConfigService
	keySource  PawChatKeySource
	attachments *PawAttachmentService
}

func NewPawImageService(config *PawConfigService, keySource PawChatKeySource, attachments ...*PawAttachmentService) *PawImageService {
	var attachmentService *PawAttachmentService
	if len(attachments) > 0 {
		attachmentService = attachments[0]
	}
	return &PawImageService{config: config, keySource: keySource, attachments: attachmentService}
}

func (s *PawImageService) ValidateGeneration(ctx context.Context, userID int64, req PawImageGenerationRequest) (*PawImageResolution, error) {
	if s == nil || s.config == nil || s.keySource == nil {
		return nil, infraerrors.ServiceUnavailable("CONFIG_UNAVAILABLE", "Paw image configuration is unavailable")
	}
	if userID <= 0 {
		return nil, infraerrors.Unauthorized("AUTH_REQUIRED", "authenticated user is required")
	}
	if req.GroupID <= 0 || strings.TrimSpace(req.ModelID) == "" {
		return nil, infraerrors.ServiceUnavailable("CONFIG_UNAVAILABLE", "Paw image configuration is unavailable")
	}
	cfg, err := s.config.GetAvailableConfig(ctx, userID)
	if err != nil {
		return nil, err
	}
	groups, err := s.config.groups.AvailableGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	group, model, ok := findPawImageSelection(cfg, groups, req.GroupID, req.ModelID)
	if !ok {
		if pawGroupExists(cfg, req.GroupID) {
			return nil, infraerrors.BadRequest("MODEL_UNAVAILABLE", "selected image model is not available in this group")
		}
		return nil, infraerrors.Forbidden("GROUP_FORBIDDEN", "selected image group is not available to this user")
	}
	if !model.ImageGeneration {
		return nil, infraerrors.ServiceUnavailable("CONFIG_UNAVAILABLE", "selected model does not support image generation")
	}
	apiKey, _, err := s.keySource.ResolvePawAPIKey(ctx, userID, group.ID)
	if err != nil || apiKey == nil {
		return nil, infraerrors.ServiceUnavailable("CONFIG_UNAVAILABLE", "Paw image credentials are unavailable")
	}
	return &PawImageResolution{APIKey: apiKey, Group: group, Model: model}, nil
}

func (s *PawImageService) ParseEditMultipart(contentType string, body []byte) (*PawImageEditRequest, error) {
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return nil, infraerrors.BadRequest("ATTACHMENT_INVALID", "image edit request must be multipart/form-data")
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return nil, infraerrors.BadRequest("ATTACHMENT_INVALID", "multipart boundary is required")
	}

	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	req := &PawImageEditRequest{ModelID: "gpt-image-2", N: 1}
	for {
		part, err := reader.NextPart()
		if err == http.ErrMissingFile || err == nil && part == nil {
			break
		}
		if err != nil {
			if err == multipart.ErrMessageTooLarge {
				return nil, infraerrors.BadRequest("ATTACHMENT_INVALID", "image edit request is too large")
			}
			if err == http.ErrNotMultipart {
				return nil, infraerrors.BadRequest("ATTACHMENT_INVALID", "image edit request must be multipart/form-data")
			}
			if strings.EqualFold(err.Error(), "EOF") {
				break
			}
			if err == nil {
				break
			}
			if strings.Contains(strings.ToLower(err.Error()), "eof") {
				break
			}
			return nil, infraerrors.BadRequest("ATTACHMENT_INVALID", "failed to read image edit multipart body")
		}

		name := strings.TrimSpace(part.FormName())
		if name == "" {
			_ = part.Close()
			continue
		}

		data, readErr := io.ReadAll(part)
		_ = part.Close()
		if readErr != nil {
			return nil, infraerrors.BadRequest("ATTACHMENT_INVALID", "failed to read image edit multipart body")
		}
		if len(data) == 0 {
			continue
		}

		if fileName := strings.TrimSpace(part.FileName()); fileName != "" {
			mimeType := strings.TrimSpace(part.Header.Get("Content-Type"))
			if mimeType == "" {
				mimeType = http.DetectContentType(data)
			} else if parsed, _, parseErr := mime.ParseMediaType(mimeType); parseErr == nil {
				mimeType = parsed
			}
			if !isPawAttachmentImageMIMEType(mimeType) {
				return nil, infraerrors.BadRequest("ATTACHMENT_INVALID", "image edit uploads must be images")
			}
			upload := PawImageEditAttachment{
				FieldName: name,
				Filename:  fileName,
				MIMEType:  strings.ToLower(strings.TrimSpace(mimeType)),
				Data:      append([]byte(nil), data...),
			}
			switch name {
			case "mask":
				req.Mask = &upload
			case "image":
				req.Images = append(req.Images, upload)
			default:
				if strings.HasPrefix(name, "image[") {
					req.Images = append(req.Images, upload)
				}
			}
			continue
		}

		value := strings.TrimSpace(string(data))
		switch name {
		case "group_id":
			if groupID, parseErr := strconv.ParseInt(value, 10, 64); parseErr == nil && groupID > 0 {
				req.GroupID = groupID
			}
		case "model":
			req.ModelID = value
		case "prompt":
			req.Prompt = value
		case "size":
			req.Size = value
		case "n":
			if n, parseErr := strconv.Atoi(value); parseErr == nil && n > 0 {
				req.N = n
			}
		}
	}

	if len(req.Images) == 0 {
		return nil, infraerrors.BadRequest("ATTACHMENT_INVALID", "image edit request requires at least one image")
	}
	return req, nil
}

func findPawImageSelection(cfg *PawConfig, groups []Group, groupID int64, modelID string) (*Group, PawModel, bool) {
	if cfg == nil {
		return nil, PawModel{}, false
	}
	for i := range cfg.Groups {
		if cfg.Groups[i].ID != groupID {
			continue
		}
		for _, model := range cfg.Groups[i].Models {
			if model.ID == modelID {
				for j := range groups {
					if groups[j].ID == groupID {
						group := groups[j]
						return &group, model, true
					}
				}
				return nil, PawModel{}, false
			}
		}
		return nil, PawModel{}, false
	}
	return nil, PawModel{}, false
}
