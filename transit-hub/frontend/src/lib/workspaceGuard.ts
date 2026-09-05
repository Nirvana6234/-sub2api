// workspaceGuard 管理路由级别的 workspace 检查缓存。
// 独立于 router 和 auth，避免循环依赖。
//
// workspaceChecked: 是否已向后端确认过当前 workspace 存在
// hasWorkspace: 确认结果（true=有活跃 workspace）
//
// 重置时机：
//   - clearAccessToken（401、手动退出）
//   - workspace 切换失败
//   - 登录新用户前

let workspaceChecked = false
let hasWorkspace = false

export function resetWorkspaceCheck() {
  workspaceChecked = false
  hasWorkspace = false
}

export function markWorkspaceActive() {
  workspaceChecked = true
  hasWorkspace = true
}

export function isWorkspaceChecked() {
  return workspaceChecked
}

export function isWorkspaceActive() {
  return hasWorkspace
}

export function setWorkspaceChecked(checked: boolean, active: boolean) {
  workspaceChecked = checked
  hasWorkspace = active
}
