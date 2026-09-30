package requestmodel

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"strings"
)

// RoutingModel 是自动分组按模型选组用的模型：JSON 的 `model`、`session.model`（精确大小写），
// 其次 multipart 的 `model` 字段或 `session` 字段里的 JSON。单机的自动分组中间件与主从分流的从节点共用。
func RoutingModel(contentType string, body []byte) string {
	if model := FromJSON(body); model != "" {
		return model
	}
	return multipartRoutingModel(contentType, body)
}

func multipartRoutingModel(contentType string, body []byte) string {
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return ""
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return ""
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return ""
		}
		if err != nil {
			return ""
		}
		fieldName := part.FormName()
		if part.FileName() != "" || (fieldName != "model" && fieldName != "session") {
			continue
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return ""
		}
		switch fieldName {
		case "model":
			return strings.TrimSpace(string(data))
		case "session":
			if model := FromJSON(data); model != "" {
				return model
			}
		}
	}
}

// DefaultAutoGroupModel 是请求体里没有模型时自动分组按路径兜底用的模型（图片入口默认 gpt-image-2）。
func DefaultAutoGroupModel(path string) string {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	switch {
	case strings.HasSuffix(path, "/images/generations"),
		strings.HasSuffix(path, "/images/edits"),
		strings.HasSuffix(path, "/images/generations/async"),
		strings.HasSuffix(path, "/images/edits/async"):
		return "gpt-image-2"
	default:
		return ""
	}
}
