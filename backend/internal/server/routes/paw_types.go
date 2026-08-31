package routes

const (
	PawErrorCodeConfigUnavailable    = "CONFIG_UNAVAILABLE"
	PawErrorCodeGroupForbidden       = "GROUP_FORBIDDEN"
	PawErrorCodeModelUnavailable     = "MODEL_UNAVAILABLE"
	PawErrorCodeReasoningUnsupported = "REASONING_UNSUPPORTED"
	PawErrorCodeAttachmentInvalid    = "ATTACHMENT_INVALID"
	PawErrorCodeQuotaExceeded        = "QUOTA_EXCEEDED"
	PawErrorCodeUpstreamUnavailable  = "UPSTREAM_UNAVAILABLE"
	PawErrorCodeAuthRequired         = "AUTH_REQUIRED"
)

type PawConfigResponse struct {
	Data PawConfigData `json:"data"`
}

type PawConfigData struct {
	User     PawUser     `json:"user"`
	Groups   []PawGroup  `json:"groups"`
	Defaults PawDefaults `json:"defaults"`
}

type PawUser struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type PawGroup struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Models      []PawModel `json:"models"`
}

type PawModel struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	OwnedBy         string                 `json:"owned_by"`
	Reasoning       PawReasoningCapability `json:"reasoning"`
	Vision          bool                   `json:"vision"`
	ImageGeneration bool                   `json:"image_generation"`
	FileInput       bool                   `json:"file_input"`
}

type PawReasoningCapability struct {
	Supported bool     `json:"supported"`
	Values    []string `json:"values"`
	Default   string   `json:"default"`
}

type PawDefaults struct {
	GroupID   int64  `json:"group_id"`
	ModelID   string `json:"model_id"`
	Reasoning string `json:"reasoning"`
}

type PawChatRequest struct {
	GroupID     int64                    `json:"group_id"`
	ModelID     string                   `json:"model_id"`
	Reasoning   string                   `json:"reasoning"`
	Messages    []PawChatMessage         `json:"messages"`
	Stream      bool                     `json:"stream"`
	Attachments []PawAttachmentReference `json:"attachments"`
}

type PawChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type PawAttachmentReference struct {
	ID string `json:"id"`
}

type PawErrorResponse struct {
	Error PawError `json:"error"`
}

type PawError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
