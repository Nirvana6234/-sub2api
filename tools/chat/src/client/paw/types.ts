import type { AgentApprovalUiMode } from "@/client/agent/session";

export interface PawUser {
  id: number;
  name: string;
  email: string;
  balance?: number;
  frozen_balance?: number;
  total_recharged?: number;
}

export type PawAnnouncementNotifyMode = "silent" | "popup";

export interface PawAnnouncement {
  id: number;
  title: string;
  content: string;
  notify_mode: PawAnnouncementNotifyMode;
  starts_at?: string;
  ends_at?: string;
  read_at?: string;
  created_at: string;
  updated_at: string;
}

export interface PawUsageDashboardStats {
  total_requests: number;
  total_input_tokens: number;
  total_output_tokens: number;
  total_cache_creation_tokens: number;
  total_cache_read_tokens: number;
  total_tokens: number;
  total_cost: number;
  total_actual_cost: number;
  today_requests: number;
  today_input_tokens: number;
  today_output_tokens: number;
  today_cache_creation_tokens: number;
  today_cache_read_tokens: number;
  today_tokens: number;
  today_cost: number;
  today_actual_cost: number;
  average_duration_ms: number;
  rpm: number;
  tpm: number;
}

export interface PawUsageTrendPoint {
  date: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  total_tokens: number;
  cost: number;
  actual_cost: number;
}

export interface PawUsageLog {
  id: number;
  model: string;
  group_id: number | null;
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  total_cost: number;
  actual_cost: number;
  rate_multiplier: number;
  request_type?: string;
  stream: boolean;
  created_at: string;
}

export interface PawUsageDashboardSnapshot {
  generated_at: string;
  start_date: string;
  end_date: string;
  granularity: string;
  trend?: PawUsageTrendPoint[];
  models?: Array<{
    model: string;
    requests: number;
    total_tokens: number;
    cost: number;
    actual_cost: number;
  }>;
  groups?: Array<{
    group_id: number;
    group_name: string;
    requests: number;
    total_tokens: number;
    cost: number;
    actual_cost: number;
  }>;
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
  platform?: string;
  rate_multiplier?: number;
  user_rate_multiplier?: number | null;
  subscription_type?: string;
  peak_rate_enabled?: boolean;
  peak_start?: string;
  peak_end?: string;
  peak_rate_multiplier?: number;
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

export interface PawPublicSettings {
  registration_enabled: boolean;
  email_verify_enabled: boolean;
  registration_email_suffix_whitelist?: string[];
  promo_code_enabled: boolean;
  invitation_code_enabled: boolean;
  turnstile_enabled?: boolean;
  turnstile_site_key?: string;
  tencent_captcha_enabled?: boolean;
  tencent_captcha_app_id?: string;
  aliyun_captcha_enabled?: boolean;
  aliyun_captcha_scene_id?: string;
  aliyun_captcha_prefix?: string;
  site_name?: string;
}

export interface PawRegisterRequest {
  email: string;
  password: string;
  verify_code?: string;
  turnstile_token?: string;
  tencent_captcha_ticket?: string;
  tencent_captcha_randstr?: string;
  promo_code?: string;
  invitation_code?: string;
  aff_code?: string;
}

export interface PawSendVerifyCodeRequest {
  email: string;
  turnstile_token?: string;
  tencent_captcha_ticket?: string;
  tencent_captcha_randstr?: string;
}

export interface PawSendVerifyCodeResponse {
  message?: string;
  countdown: number;
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

export interface PawPaymentMethodLimit {
  currency: string;
  display_name: string;
  single_min: number;
  single_max: number;
  fee_rate: number;
  available: boolean;
}

export interface PawPaymentCheckoutInfo {
  methods: Record<string, PawPaymentMethodLimit>;
  global_min: number;
  global_max: number;
  balance_disabled: boolean;
  balance_recharge_multiplier: number;
  recharge_fee_rate: number;
  help_text: string;
  help_image_url: string;
}

export type PawPaymentOrderStatus =
  | "PENDING"
  | "PAID"
  | "RECHARGING"
  | "COMPLETED"
  | "EXPIRED"
  | "CANCELLED"
  | "FAILED"
  | string;

export interface PawPaymentCreateOrderRequest {
  amount: number;
  payment_type: string;
  order_type: "balance";
  is_mobile: boolean;
  payment_source?: string;
}

export interface PawPaymentOrderCreateResult {
  order_id: number;
  amount: number;
  pay_amount: number;
  fee_rate: number;
  currency?: string;
  payment_type?: string;
  qr_code?: string;
  pay_url?: string;
  out_trade_no?: string;
  expires_at: string;
  payment_mode?: string;
}

export interface PawPaymentOrder {
  id: number;
  amount: number;
  pay_amount: number;
  fee_rate: number;
  currency?: string;
  payment_type: string;
  out_trade_no: string;
  status: PawPaymentOrderStatus;
  order_type: string;
  expires_at: string;
  paid_at?: string;
  completed_at?: string;
}

export interface PawConversationMessage {
  id: string;
  role: "system" | "user" | "assistant";
  content: string;
  model?: string;
  reasoningContent?: string;
  agentPanels?: PawAgentPanels;
  attachments?: PawAttachment[];
  images?: string[];
  pinned?: boolean;
  error?: boolean;
  /** Runtime-only marker used to decide which messages are safe to compact on disk. */
  turnStatus?: "active" | "complete";
  /**
   * 只有 agent 轮次（`beginAgentTurn`）产生的 assistant 消息才是 `true`。
   * 用来给持久化压缩把关：普通 Paw 对话/图片消息完成后同样会有
   * `turnStatus: "complete"`，但它们的 `reasoningContent` 是要保留的正文，不是
   * 该在回合结束后清掉的 agent 中间数据——`conversationCompression.ts` 的
   * "completed" 档只在这个字段为 `true` 时才剥离 reasoning/agentPanels。
   */
  agentTurn?: boolean;
  createdAt: number;
  updatedAt: number;
}

export interface PawAgentPlan {
  explanation?: string | null;
  steps: unknown[];
  delta?: string;
}

export interface PawAgentFileChange {
  itemId: string;
  changes?: unknown;
  output?: string;
}

export interface PawAgentTerminalInteraction {
  itemId: string;
  processId: string;
  stdin: string;
  createdAt: number;
}

export interface PawAgentNotification {
  method: string;
  message: string;
  raw: unknown;
  createdAt: number;
}

export interface PawAgentFileSearch {
  sessionId: string;
  query: string;
  files: unknown[];
  completed: boolean;
  updatedAt: number;
}

export interface PawAgentApprovalReview {
  reviewId: string;
  method: string;
  raw: unknown;
  updatedAt: number;
}

export interface PawAgentPanels {
  plan?: PawAgentPlan;
  diff?: string;
  fileChanges?: Record<string, PawAgentFileChange>;
  terminalInteractions?: PawAgentTerminalInteraction[];
  moderationMetadata?: unknown[];
  notifications?: PawAgentNotification[];
  fileSearches?: Record<string, PawAgentFileSearch>;
  approvalReviews?: Record<string, PawAgentApprovalReview>;
}

export interface PawAgentPanelsPatch {
  plan?: PawAgentPlan;
  diff?: string;
  fileChanges?: Record<string, PawAgentFileChange>;
  terminalInteractions?: PawAgentTerminalInteraction[];
  moderationMetadata?: unknown[];
  notifications?: PawAgentNotification[];
  fileSearches?: Record<string, PawAgentFileSearch>;
  approvalReviews?: Record<string, PawAgentApprovalReview>;
}

export interface PawConversation {
  id: string;
  title: string;
  draft: string;
  createdAt: number;
  updatedAt: number;
  contextStartIndex?: number;
  messages: PawConversationMessage[];
  /**
   * agent 的工作目录，只在桌面端有意义。**发消息前可以随便重选**——只是个草稿，
   * 选错了、想换都行，这时候还没起真正的会话，没有任何代价。
   * 真正锁死看 `agentCwdLocked`。
   */
  agentCwd?: string;
  /**
   * `agentCwd` 是不是已经锁死了。**真正"开启会话"发生在第一次成功发消息**
   * （起了一条真的 codex thread）——那一刻起才锁定，之前光选目录不算数、
   * 可以随时改主意重选。锁定之后没有"更换"入口，想用别的目录就开新对话。
   */
  agentCwdLocked?: boolean;
  /** 审批模式；未设置时按"完全控制"（`full`）处理，是 composer 两态切换的默认值。 */
  agentApprovalMode?: AgentApprovalUiMode;
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
