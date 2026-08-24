export type NotificationChannel = 'dingtalk' | 'wecom' | 'qq' | 'feishu' | 'telegram'
export type NotificationTemplateFormat = 'text' | 'markdown' | 'html'

export type TestNotificationChannelPayload = {
  channel: NotificationChannel
  webhook?: string
  secret?: string
  telegramBotToken?: string
  telegramChatId?: string
  telegramProxyUrl?: string
  qqAppId?: string
  qqClientSecret?: string
  qqUserOpenId?: string
  qqGroupOpenId?: string
}

export type TestNotificationChannelResponse = {
  success: boolean
  message: string
}

export type NotificationChannelSettings = {
  dingtalk: DingtalkChannelSettings[]
  wecom: WebhookChannelSettings[]
  qq: QQChannelSettings[]
  feishu: WebhookChannelSettings[]
  telegram: TelegramChannelSettings[]
}

export type StrategySettings = {
  enableRefreshInterval: boolean
  refreshInterval: number
  enableBalanceWarning: boolean
  defaultBalanceThreshold: number
  balanceNotifyBotIds: string[]
  balanceTemplate: string
  balanceTemplateFormat?: NotificationTemplateFormat
  enableMultiplierAlert: boolean
  multiplierNotifyBotIds: string[]
  multiplierTemplate: string
  multiplierTemplateFormat?: NotificationTemplateFormat
  /**
   * 每日运营报告。这四个字段后端一直支持，但之前前端没有对应界面，
   * 于是保存设置时会把它们整个丢掉——线上 enableDailyReport 从来没被打开过。
   */
  enableDailyReport: boolean
  /** 推送时刻，Asia/Shanghai 的 HH:MM。 */
  dailyReportTime: string
  dailyReportBotIds: string[]
  dailyReportFormat?: NotificationTemplateFormat
}

/** POST /api/daily-report/send-now 的响应。 */
export type DailyReportSendResult = {
  sent: boolean
  botCount: number
  /** 这次发出去的正文，前端可以就地展示，省得再去翻聊天软件。 */
  report: string
}

export type DailyReportPreview = {
  report: string
  format?: NotificationTemplateFormat
}

export type DingtalkChannelSettings = {
  id: string
  name: string
  enabled: boolean
  webhook: string
  secret: string
}

export type WebhookChannelSettings = {
  id: string
  name: string
  enabled: boolean
  webhook: string
  secret: string
}

export type TelegramChannelSettings = {
  id: string
  name: string
  enabled: boolean
  botToken: string
  chatId: string
  proxyUrl: string
}

export type QQChannelSettings = {
  id: string
  name: string
  enabled: boolean
  appId: string
  clientSecret: string
  userOpenId: string
  groupOpenId?: string
}

export type SmtpTlsMode = 'implicit' | 'starttls'

export type SmtpSettings = {
  host: string
  port: number
  username: string
  fromEmail: string
  fromName: string
  tlsMode: SmtpTlsMode
  passwordConfigured: boolean
  updatedAt: string | null
}

export type SaveSmtpSettingsPayload = {
  host: string
  port: number
  username: string
  password?: string
  fromEmail: string
  fromName: string
  tlsMode: SmtpTlsMode
}

export type TestSmtpEmailPayload = {
  recipientEmail: string
}

export type TestSmtpEmailResponse = {
  success: boolean
  message: string
}

export type EmailTemplate = {
  id: string
  name: string
  subject: string
  htmlBody: string
  isBuiltIn: boolean
  createdAt: string | null
  updatedAt: string | null
}

export type SaveEmailTemplatePayload = {
  name: string
  subject: string
  htmlBody: string
}

export type TestEmailTemplatePayload = {
  recipientEmail: string
}

export type TestEmailTemplateResponse = {
  success: boolean
  message: string
}
