// 图表统一色板：以品牌天空蓝打头，第二、三位与三种用法的识别色（青绿 / 紫）同源，
// 其余用低饱和的暖色补足，避免 chart.js 默认红绿蓝橙那种"撞色"感。
// 分布图、排行图、趋势图都从这里取色，改色板只改这一处。
export const CHART_SERIES_COLORS = [
  '#169bd0',
  '#0f9f8f',
  '#6d5cf0',
  '#e0a458',
  '#e07a5f',
  '#5b8def',
  '#9bbf6a',
  '#c084a8',
  '#4fb3bf',
  '#a8927a',
  '#7d8fb3',
  '#d4b44a',
] as const

/** Token 趋势图各条线的颜色：输入/输出用主色，缓存用弱化色，命中率用紫色虚线。 */
export const TOKEN_TREND_COLORS = {
  input: '#169bd0',
  output: '#0f9f8f',
  cacheCreation: '#e0a458',
  cacheRead: '#7fa7bd',
  cacheHitRate: '#6d5cf0',
} as const
