// 从模型名推断厂商和用途，只用于列表里的图标与小标签（纯展示，不参与计费或路由）。
export type ModelKind = 'image' | 'code' | 'chat'

export interface ModelDescription {
  vendor: string | null
  kind: ModelKind
}

const VENDOR_RULES: [RegExp, string][] = [
  [/claude|anthropic/i, 'Claude'],
  [/gemini|imagen/i, 'Gemini'],
  [/grok/i, 'Grok'],
  [/deepseek/i, 'DeepSeek'],
  [/glm|zhipu/i, 'GLM'],
  [/kimi|moonshot/i, 'Kimi'],
  [/qwen/i, 'Qwen'],
  [/minimax/i, 'MiniMax'],
  [/gpt|chatgpt|dall-e|codex|^o\d/i, 'OpenAI'],
]

const IMAGE_PATTERN = /image|dall-e|imagen|flux|seedream|midjourney|stable-diffusion/i
const CODE_PATTERN = /codex|coder/i

export function describeModel(model: string | null | undefined): ModelDescription {
  const name = (model || '').trim()
  const vendor = VENDOR_RULES.find(([pattern]) => pattern.test(name))?.[1] ?? null
  const kind: ModelKind = IMAGE_PATTERN.test(name) ? 'image' : CODE_PATTERN.test(name) ? 'code' : 'chat'
  return { vendor, kind }
}
