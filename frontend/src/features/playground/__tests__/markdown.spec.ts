import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { renderPlaygroundMarkdown, stripMathDelimitersForCopy } from '../markdown'

describe('renderPlaygroundMarkdown', () => {
  it('renders inline $...$ math as KaTeX HTML', () => {
    const html = renderPlaygroundMarkdown('质能方程 $E = mc^2$ 很有名。')
    expect(html).toContain('class="katex"')
    expect(html).not.toContain('$E = mc^2$')
  })

  it('renders block $$...$$ math in display mode', () => {
    const html = renderPlaygroundMarkdown('$$\\int_0^1 x^2 dx$$')
    expect(html).toContain('katex-display')
  })

  it('still escapes unsafe HTML in surrounding markdown', () => {
    const html = renderPlaygroundMarkdown('<img src=x onerror=alert(1)> and $a+b$')
    expect(html).not.toContain('onerror')
    expect(html).toContain('class="katex"')
  })

  it('leaves plain text without math delimiters untouched', () => {
    const html = renderPlaygroundMarkdown('price is $5 today')
    expect(html).not.toContain('katex')
  })

  it('renders \\( \\) inline math', () => {
    const html = renderPlaygroundMarkdown('欧拉公式 \\(e^{i\\pi} + 1 = 0\\) 很优雅。')
    expect(html).toContain('class="katex"')
    expect(html).not.toContain('\\(e^')
  })

  it('renders \\[ \\] as display math', () => {
    const html = renderPlaygroundMarkdown('\\[\nx = \\frac{-b \\pm \\sqrt{b^2-4ac}}{2a}\n\\]')
    expect(html).toContain('katex-display')
  })

  it('renders single-line \\[ \\] inline within a sentence', () => {
    const html = renderPlaygroundMarkdown('结果是 \\[x^2\\] 没错。')
    expect(html).toContain('class="katex"')
  })

  it('does not touch \\( \\) or \\[ \\] literals inside fenced code blocks', () => {
    const html = renderPlaygroundMarkdown('```js\nconst re = /\\(a\\)/\n```')
    expect(html).not.toContain('katex')
    expect(html).toContain('const re')
  })

  it('does not touch \\( \\) literals inside inline code spans', () => {
    const html = renderPlaygroundMarkdown('use `\\(literal\\)` here and $x+1$ there')
    expect(html).toContain('<code>')
    expect(html.match(/class="katex"/g)?.length).toBe(1)
  })

  // marked-katex-extension 的单 $ 行内正则要求收尾字符落在一份很窄的标点白名单
  // 里，分号/右括号/引号等常见收尾字符都不在其中。这里换成自己抽占位符渲染后，
  // 不应该再受这份白名单限制。
  it('renders formulas immediately followed by punctuation not in the old whitelist', () => {
    for (const trailing of [';', '；', ')', ']', '"', "'", '，']) {
      const html = renderPlaygroundMarkdown(`定义 $d_Q$${trailing}下一句`)
      expect(html, `trailing char ${JSON.stringify(trailing)} should still render`).toContain('class="katex"')
    }
  })

  it('renders a bulleted list where every item ends with a semicolon', () => {
    const source = [
      '- $d_Q$;',
      '- $\\ell_i$;',
      '- $\\gamma_X^0$;',
    ].join('\n')
    const html = renderPlaygroundMarkdown(source)
    expect(html.match(/class="katex"/g)?.length).toBe(3)
  })

  // DOMPurify 默认会削掉 MathML 的 <annotation>（连同 encoding 属性），只留渲染
  // 用的 <mrow> 等标签。放行之后手动选中公式复制才能落到这份干净的 LaTeX 源码上，
  // 而不是纯视觉渲染分支打散的乱码文本。
  it('keeps the katex-mathml annotation branch alive through DOMPurify', () => {
    const html = renderPlaygroundMarkdown('$E = mc^2$')
    expect(html).toContain('<annotation encoding="application/x-tex">')
    expect(html).toContain('E = mc^2')
  })

  it('ships a stylesheet that disables selection on the visual katex-html branch', () => {
    const cssPath = resolve(__dirname, '../katex-overrides.css')
    const css = readFileSync(cssPath, 'utf-8')
    expect(css).toMatch(/\.katex-html\s*{[^}]*user-select:\s*none/)
  })
})

describe('stripMathDelimitersForCopy', () => {
  it('strips \\( \\) delimiters, leaving the formula readable', () => {
    expect(stripMathDelimitersForCopy('欧拉公式 \\(e^{i\\pi} + 1 = 0\\) 很优雅。'))
      .toBe('欧拉公式 e^{i\\pi} + 1 = 0 很优雅。')
  })

  it('strips \\[ \\] delimiters', () => {
    expect(stripMathDelimitersForCopy('结果是 \\[x^2\\] 没错。')).toBe('结果是 x^2 没错。')
  })

  it('strips $$...$$ block delimiters', () => {
    expect(stripMathDelimitersForCopy('$$E = mc^2$$')).toBe('E = mc^2')
  })

  it('leaves single $...$ untouched to avoid mangling currency text', () => {
    expect(stripMathDelimitersForCopy('price is $5 and $10')).toBe('price is $5 and $10')
    expect(stripMathDelimitersForCopy('质能方程 $E = mc^2$ 很有名。')).toBe('质能方程 $E = mc^2$ 很有名。')
  })

  it('does not touch delimiter-looking text inside fenced code blocks', () => {
    const source = '```js\nconst re = /\\(a\\)/\n```'
    expect(stripMathDelimitersForCopy(source)).toBe(source)
  })

  it('does not touch delimiter-looking text inside inline code spans', () => {
    const source = 'use `\\(literal\\)` here and \\(x+1\\) there'
    expect(stripMathDelimitersForCopy(source)).toBe('use `\\(literal\\)` here and x+1 there')
  })
})
