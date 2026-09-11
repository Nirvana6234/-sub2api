// 仪表盘 admin 登录相关类型，支持 sub2api 和 new-api 两种平台。

export type DashboardAdminPlatform = 'sub2api' | 'newapi'

export type DashboardAdminAuthMethod = 'password' | 'token' | 'admin_key'

/** 保留旧类型名，避免影响现有调用方。 */
export type Sub2apiAuthMethod = DashboardAdminAuthMethod

/** 后端返回的 admin 登录状态，用于决定是否弹窗与展示凭证过期时间。 */
export interface DashboardAdminStatus {
  authenticated: boolean
  platform?: string
  baseUrl?: string
  authMethod?: string
  identity?: string
  /** 登录凭证（access token）过期的毫秒时间戳，临期自动刷新；null 表示未知。 */
  expiresAt?: number | null
}

/** 登录弹窗提交的表单，覆盖两种登录方式所需字段。 */
export interface DashboardAdminLoginForm {
  platform: DashboardAdminPlatform
  siteUrl: string
  authMethod: Sub2apiAuthMethod
  email?: string
  password?: string
  accessToken?: string
  refreshToken?: string
  tokenType?: string
  adminKey?: string
  userId?: string
}
