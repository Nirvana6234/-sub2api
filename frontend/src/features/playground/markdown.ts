import DOMPurify from 'dompurify'
import { marked } from 'marked'
import katex from 'katex'
import 'katex/dist/katex.min.css'
import './katex-overrides.css'

// 代码块 / 行内代码要跳过，不能把示例代码里字面的 $ \( \) \[ \]（比如正则、Shell
// 转义）当成公式定界符处理掉。
const CODE_SEGMENT_PATTERN = /(```[\s\S]*?```|~~~[\s\S]*?~~~|`[^`\n]*`)/g

// 公式匹配顺序很重要：先试双字符定界符（$$、\[ \]），避免被单 $ 分支提前截断。
// 单个 $...$ 只在开头不紧跟空白、结尾不紧跟数字时才匹配，用来降低跟货币连用
// （"$5 和 $10"）时的误判概率——不追求完美，只求不比之前更差。
const MATH_PATTERN = /\$\$([\s\S]+?)\$\$|\\\[([\s\S]+?)\\\]|\\\(([\s\S]+?)\\\)|\$(?!\s)((?:\\.|[^\\\n$])+?)\$(?!\d)/g

function renderMathToHtml(tex: string, displayMode: boolean): string {
  try {
    return katex.renderToString(tex, { throwOnError: false, displayMode, output: 'htmlAndMathml' })
  } catch {
    return displayMode ? `\\[${tex}\\]` : `\\(${tex}\\)`
  }
}

// 公式渲染完全不走 marked 自身的行内分词器：marked-katex-extension 的单 $ 行内
// 正则要求公式后面紧跟的字符必须落在一份很窄的标点白名单里，分号、右括号、引号
// 等常见收尾字符都不在其中——公式后面紧跟个分号就整段原样显示、不渲染。
// 这里改成喂给 marked 之前先把公式抽走换成纯字母数字的占位符（marked 的
// strong/em/autolink 等行内规则不会误伤纯字母数字文本，也不需要转义），
// marked 处理完 markdown 排版后，再把占位符换回真正渲染好的 KaTeX HTML，
// 公式能不能被识别完全由这里的正则决定，不再受 marked 分词边界的影响。
function extractMathPlaceholders(source: string): { text: string, restore: (html: string) => string } {
  const nonce = Math.random().toString(36).slice(2, 10)
  const renders: string[] = []
  const token = (index: number) => `MATHPLACEHOLDER${nonce}${index}ENDMATH`

  const text = source
    .split(CODE_SEGMENT_PATTERN)
    .map((segment, index) => {
      // split() 对带捕获组的正则会把命中的代码段插回结果数组，位于奇数下标；
      // 这些原样跳过，只处理偶数下标的普通文本段。
      if (index % 2 === 1) return segment
      return segment.replace(
        MATH_PATTERN,
        (match, dollarBlock?: string, bracketBlock?: string, parenInline?: string, dollarInline?: string) => {
          const displayMode = dollarBlock !== undefined || bracketBlock !== undefined
          const tex = (dollarBlock ?? bracketBlock ?? parenInline ?? dollarInline ?? '').trim()
          if (!tex) return match
          const placeholderIndex = renders.push(renderMathToHtml(tex, displayMode)) - 1
          return token(placeholderIndex)
        },
      )
    })
    .join('')

  return {
    text,
    restore: (html: string) => renders.reduce((acc, rendered, index) => acc.split(token(index)).join(rendered), html),
  }
}

export function renderPlaygroundMarkdown(source: string): string {
  const { text, restore } = extractMathPlaceholders(source || '')
  const html = restore(marked.parse(text) as string)
  // DOMPurify 默认不放行 MathML 的 <semantics>/<annotation>（连同 encoding 属性）。
  // KaTeX 把公式的原始 LaTeX 源码就存在 <annotation encoding="application/x-tex">
  // 里，专门给屏幕阅读器和"选中复制"用；被 DOMPurify 削掉之后，手动拖选公式再
  // 复制就只能落到纯视觉渲染的 .katex-html 分支，拿到的是按视觉顺序打散的乱码
  // 文本。这里显式放行这两个标签和属性，配合 katex-overrides.css 里禁掉
  // .katex-html 的选中，才能让"选中公式并复制"拿到干净的 LaTeX 源码。
  return DOMPurify.sanitize(html, {
    ADD_TAGS: ['semantics', 'annotation'],
    ADD_ATTR: ['encoding'],
  })
}

// "复制消息"按钮拿到的是未渲染的原始 markdown，公式还包着 $ $$ \( \) \[ \] 定界符，
// 贴到别处一堆符号很难看。这里只剥掉 $$...$$ / \(...\) / \[...\] 三种定界符，
// 保留公式内容本身（简单公式如 "E = mc^2" 剥完就是干净的可读文本，复杂的还是 LaTeX
// 命令但至少不再被定界符包住）。故意不处理单个 $...$：和价格连用时（"$5 和 $10"）
// 无法安全区分货币和公式，宁可保留原样也不误删用户的美元符号。
const COPY_MATH_DELIMITER_PATTERN = /\$\$([\s\S]+?)\$\$|\\\[([\s\S]+?)\\\]|\\\(([\s\S]+?)\\\)/g

export function stripMathDelimitersForCopy(source: string): string {
  return source
    .split(CODE_SEGMENT_PATTERN)
    .map((segment, index) => {
      if (index % 2 === 1) return segment
      return segment.replace(COPY_MATH_DELIMITER_PATTERN, (_match, block?: string, bracket?: string, paren?: string) =>
        (block ?? bracket ?? paren ?? '').trim())
    })
    .join('')
}
