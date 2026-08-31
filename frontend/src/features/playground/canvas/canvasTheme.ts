export const canvasTheme = {
  light: {
    background: '#f7f8fa',
    dot: '#c5cbd5',
    line: '#e4e7ec',
    node: '#ffffff',
    nodeBorder: '#d7dce5',
    active: '#0f766e',
    text: '#17202a',
    muted: '#667085',
  },
  dark: {
    background: '#111827',
    dot: '#4b5563',
    line: '#263244',
    node: '#1f2937',
    nodeBorder: '#3d4b60',
    active: '#5eead4',
    text: '#f8fafc',
    muted: '#a8b1c2',
  },
} as const

export function getCanvasTheme(isDark: boolean) {
  return isDark ? canvasTheme.dark : canvasTheme.light
}
