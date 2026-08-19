// 纯函数：把一组数值转换成 SVG 折线/面积图所需的几何路径。
// 不依赖任何框架，便于在组件里以 computed 形式调用，也方便单测。

export interface ChartPoint {
  x: number
  y: number
}

export interface ChartGeometry {
  width: number
  height: number
  /** 平滑折线路径。 */
  linePath: string
  /** 在折线下方闭合到基线的面积路径。 */
  areaPath: string
  /** 每个数据点在视图坐标系中的位置。 */
  points: ChartPoint[]
}

/**
 * 使用 Catmull-Rom 转三次贝塞尔，生成平滑曲线。
 * 比直线段更适合趋势展示，且对端点做了夹紧处理避免越界。
 */
export function buildSmoothPath(points: ChartPoint[]): string {
  if (points.length === 0) return ''
  if (points.length === 1) return `M ${points[0].x} ${points[0].y}`

  const segments: string[] = [`M ${points[0].x} ${points[0].y}`]
  for (let i = 0; i < points.length - 1; i += 1) {
    const p0 = points[i - 1] ?? points[i]
    const p1 = points[i]
    const p2 = points[i + 1]
    const p3 = points[i + 2] ?? p2

    const cp1x = p1.x + (p2.x - p0.x) / 6
    const cp1y = p1.y + (p2.y - p0.y) / 6
    const cp2x = p2.x - (p3.x - p1.x) / 6
    const cp2y = p2.y - (p3.y - p1.y) / 6

    segments.push(`C ${cp1x} ${cp1y}, ${cp2x} ${cp2y}, ${p2.x} ${p2.y}`)
  }
  return segments.join(' ')
}

/**
 * 根据数值序列与画布尺寸计算图表几何。
 * @param values  数值序列
 * @param width   视图宽度（像素）
 * @param height  视图高度（像素）
 * @param padding 上下/左右内边距，避免曲线贴边
 */
export function buildChartGeometry(
  values: number[],
  width: number,
  height: number,
  padding: number,
): ChartGeometry {
  if (values.length === 0) {
    return { width, height, linePath: '', areaPath: '', points: [] }
  }

  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min || 1
  const innerWidth = Math.max(1, width - padding * 2)
  const innerHeight = Math.max(1, height - padding * 2)
  const baseline = height - padding

  const points: ChartPoint[] = values.map((value, index) => {
    const x =
      values.length === 1
        ? padding + innerWidth / 2
        : padding + (index / (values.length - 1)) * innerWidth
    const y = padding + innerHeight - ((value - min) / span) * innerHeight
    return { x, y }
  })

  const linePath = buildSmoothPath(points)
  const first = points[0]
  const last = points[points.length - 1]
  const areaPath = linePath
    ? `${linePath} L ${last.x} ${baseline} L ${first.x} ${baseline} Z`
    : ''

  return { width, height, linePath, areaPath, points }
}
