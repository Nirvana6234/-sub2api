import DOMPurify from 'dompurify'
import { marked } from 'marked'

export function renderPlaygroundMarkdown(source: string): string {
  const html = marked.parse(source || '') as string
  return DOMPurify.sanitize(html)
}
