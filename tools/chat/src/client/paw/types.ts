export interface PawUser {
  id: number;
  name: string;
  email: string;
}

export interface PawReasoningCapability {
  supported: boolean;
  values: string[];
  default: string;
}

export interface PawModel {
  id: string;
  name: string;
  owned_by: string;
  reasoning: PawReasoningCapability;
  vision: boolean;
  image_generation: boolean;
  file_input: boolean;
}

export interface PawGroup {
  id: number;
  name: string;
  description: string;
  models: PawModel[];
}

export interface PawDefaults {
  group_id: number;
  model_id: string;
  reasoning: string;
}

export interface PawConfigData {
  user: PawUser;
  groups: PawGroup[];
  defaults: PawDefaults;
}

export interface PawConfigResponse {
  data: PawConfigData;
}

export interface PawAttachmentReference {
  id: string;
}

export interface PawChatMessagePartText {
  type: "text";
  text: string;
}

export interface PawChatMessagePartImage {
  type: "image_url";
  image_url: { url: string };
}

export interface PawChatMessagePartFile {
  type: "file";
  file: { filename: string; file_data: string };
}

export type PawChatMessageContent =
  | string
  | Array<PawChatMessagePartText | PawChatMessagePartImage | PawChatMessagePartFile>;

export interface PawChatMessage {
  role: "system" | "user" | "assistant" | "tool";
  content: PawChatMessageContent;
}

export interface PawChatRequest {
  group_id: number;
  model_id: string;
  reasoning?: string;
  messages: PawChatMessage[];
  stream: boolean;
  attachments?: PawAttachmentReference[];
}

export interface PawCompletionResult {
  content: string;
  reasoningContent: string;
  finishReason: string | null;
}

export interface PawStreamDelta {
  contentDelta: string;
  reasoningDelta: string;
  finishReason: string | null;
}

export interface PawAttachment {
  id: string;
  filename: string;
  mime_type: string;
  size: number;
  expires_at: string;
  previewUrl?: string;
}

export interface PawAttachmentResponse {
  data: PawAttachment;
}

export interface PawImageResult {
  url?: string;
  b64_json?: string;
  revised_prompt?: string;
}

export interface PawImageGenerationResponse {
  created?: number;
  data: PawImageResult[];
}

export type PawImageSize =
  | "1024x1024"
  | "1792x1024"
  | "1024x1792"
  | "768x1344"
  | "864x1152"
  | "1344x768"
  | "1152x864"
  | "1440x720"
  | "720x1440";

export interface PawError {
  code: string;
  message: string;
}

export interface PawErrorResponse {
  error: PawError;
}

export interface PawLoginResponse {
  access_token: string;
  refresh_token?: string;
  expires_in?: number;
  token_type?: string;
  user?: PawUser;
}

export interface PawRefreshResponse {
  access_token: string;
  refresh_token: string;
  expires_in: number;
  token_type?: string;
}

export interface PawSession {
  accessToken: string;
  refreshToken?: string;
  expiresAt?: number;
  user?: PawUser;
}

export interface PawConversationMessage {
  id: string;
  role: "system" | "user" | "assistant";
  content: string;
  model?: string;
  reasoningContent?: string;
  attachments?: PawAttachment[];
  images?: string[];
  pinned?: boolean;
  error?: boolean;
  createdAt: number;
  updatedAt: number;
}

export interface PawConversation {
  id: string;
  title: string;
  draft: string;
  createdAt: number;
  updatedAt: number;
  contextStartIndex?: number;
  messages: PawConversationMessage[];
}

export interface PawPrompt {
  id: string;
  title: string;
  content: string;
  createdAt: number;
  isUser?: boolean;
}

export type PawSubmitKey = "enter" | "shift-enter" | "ctrl-enter" | "alt-enter";

export interface PawSelectionState {
  groupId: number | null;
  modelId: string;
  reasoning: string;
}

export type PawMessage = PawConversationMessage;
