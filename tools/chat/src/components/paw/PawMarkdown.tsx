"use client";

import {
  isValidElement,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import ReactMarkdown from "react-markdown";
import rehypeHighlight from "rehype-highlight";
import rehypeKatex from "rehype-katex";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import "katex/dist/katex.min.css";
import {
  PawCheckIcon,
  PawCloseIcon,
  PawCopyIcon,
  PawMaximizeIcon,
} from "./PawIcons";

interface PawMarkdownProps {
  content: string;
  loading?: boolean;
}

function textFromNode(node: ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(textFromNode).join("");
  if (isValidElement<{ children?: ReactNode }>(node)) {
    return textFromNode(node.props.children);
  }
  return "";
}

function MermaidBlock({ code }: { code: string }) {
  const id = `paw-mermaid-${useId().replace(/[^a-zA-Z0-9_-]/g, "")}`;
  const ref = useRef<HTMLDivElement>(null);
  const [error, setError] = useState(false);
  const [rendered, setRendered] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setError(false);
    setRendered(false);
    if (!ref.current || !code.trim()) return;
    ref.current.replaceChildren();

    void import("mermaid")
      .then(async (module) => {
        const mermaid = module.default;
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: "strict",
          theme: document.documentElement.classList.contains("paw-dark")
            ? "dark"
            : "default",
        });
        const result = await mermaid.render(id, code);
        if (cancelled || !ref.current) return;
        ref.current.innerHTML = result.svg;
        result.bindFunctions?.(ref.current);
        setRendered(true);
      })
      .catch(() => {
        if (!cancelled) setError(true);
      });

    return () => {
      cancelled = true;
    };
  }, [code, id]);

  if (error) {
    return (
      <div className="paw-markdown-artifact-error">
        Mermaid 图表暂时无法渲染，请查看代码内容。
      </div>
    );
  }

  return (
    <div className="paw-markdown-mermaid" aria-label="Mermaid 图表">
      <div ref={ref} />
      {!rendered ? <span>正在渲染图表...</span> : null}
    </div>
  );
}

function HtmlPreview({ code, onClose }: { code: string; onClose: () => void }) {
  useEffect(() => {
    const handleKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  return (
    <div
      className="paw-artifact-overlay"
      role="presentation"
      onMouseDown={(event) => {
        if (event.currentTarget === event.target) onClose();
      }}
    >
      <section
        className="paw-artifact"
        role="dialog"
        aria-modal="true"
        aria-label="HTML 预览"
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="paw-artifact-head">
          <strong>HTML 预览</strong>
          <button type="button" className="paw-icon-button" onClick={onClose} aria-label="关闭预览">
            <PawCloseIcon width={16} height={16} />
          </button>
        </header>
        <iframe
          className="paw-artifact-frame"
          title="HTML 预览"
          sandbox="allow-scripts"
          srcDoc={code}
        />
      </section>
    </div>
  );
}

function CodeBlock({
  className,
  children,
}: {
  className?: string;
  children?: React.ReactNode;
}) {
  const [copied, setCopied] = useState(false);
  const [previewOpen, setPreviewOpen] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const code = textFromNode(children).replace(/\n$/, "");
  const language = className?.match(/language-([\w-]+)/)?.[1] || "text";
  const collapsible = code.split("\n").length > 24;

  async function copyCode() {
    if (!navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      setCopied(false);
    }
  }

  if (language === "mermaid") {
    return <MermaidBlock code={code} />;
  }

  return (
    <>
      <div className="paw-markdown-code">
      <div className="paw-markdown-code-head">
        <span>{language}</span>
        <div className="paw-markdown-code-actions">
          {language === "html" ? (
            <button
              type="button"
              className="paw-icon-button"
              onClick={() => setPreviewOpen(true)}
              title="预览 HTML"
              aria-label="预览 HTML"
            >
              <PawMaximizeIcon width={14} height={14} />
            </button>
          ) : null}
          <button
            type="button"
            className="paw-icon-button paw-markdown-code-copy"
            onClick={() => void copyCode()}
            title={copied ? "已复制" : "复制代码"}
            aria-label={copied ? "已复制" : "复制代码"}
          >
            {copied ? (
              <PawCheckIcon width={14} height={14} />
            ) : (
              <PawCopyIcon width={14} height={14} />
            )}
          </button>
        </div>
      </div>
      <pre className={collapsible && !expanded ? "collapsed" : ""}>
        <code className={className}>{children}</code>
      </pre>
      {collapsible ? (
        <button
          type="button"
          className="paw-markdown-code-toggle"
          onClick={() => setExpanded((value) => !value)}
        >
          {expanded ? "收起代码" : "展开更多"}
        </button>
      ) : null}
      </div>
      {previewOpen ? <HtmlPreview code={code} onClose={() => setPreviewOpen(false)} /> : null}
    </>
  );
}

function normalizeMarkdown(content: string): string {
  const escaped = content
    .replace(/\\\[([\s\S]*?[^\\])\\\]/g, "$$$1$$")
    .replace(/\\\((.*?)\\\)/g, "$$$1$");
  if (/```/.test(escaped)) return escaped;
  if (/<!(?:DOCTYPE html)|<html[\s>]|<svg[\s>]|<\?xml/i.test(escaped)) {
    return `\`\`\`html\n${escaped}\n\`\`\``;
  }
  return escaped;
}

export function PawMarkdown({ content, loading = false }: PawMarkdownProps) {
  const normalizedContent = useMemo(
    () => normalizeMarkdown(content.replace(/\r\n/g, "\n")),
    [content],
  );

  if (loading && !normalizedContent.trim()) {
    return (
      <div className="paw-markdown-loading" aria-label="正在生成">
        <span />
        <span />
        <span />
      </div>
    );
  }

  return (
    <div className="paw-markdown">
      <ReactMarkdown
        remarkPlugins={[
          remarkMath,
          remarkGfm,
          remarkBreaks as unknown as typeof remarkGfm,
        ]}
        rehypePlugins={[
          rehypeKatex,
          [
            rehypeHighlight,
            {
              detect: false,
              ignoreMissing: true,
            },
          ],
        ]}
        components={{
          code(props) {
            const { className, children, ...rest } = props;
            const node = props.node as
              | {
                  position?: {
                    start?: { line?: number };
                    end?: { line?: number };
                  };
                }
              | undefined;
            const inline =
              node?.position?.start?.line !== undefined &&
              node.position.end?.line !== undefined
                ? node.position.start.line === node.position.end.line
                : Boolean((props as unknown as { inline?: boolean }).inline);
            if (inline) {
              return (
                <code className="paw-markdown-inline-code" {...rest}>
                  {children}
                </code>
              );
            }
            return (
              <CodeBlock className={className} {...rest}>
                {children}
              </CodeBlock>
            );
          },
          a({ href, children, ...props }) {
            const target = href ?? "";
            if (/\.(aac|m4a|mp3|ogg|opus|wav)(?:$|[?#])/i.test(target)) {
              return (
                <audio controls preload="metadata" src={target}>
                  {children}
                </audio>
              );
            }
            if (/\.(3gp|avi|m4v|mkv|mov|mp4|mpeg|ogv|webm)(?:$|[?#])/i.test(target)) {
              return (
                <video controls preload="metadata" className="paw-markdown-video">
                  <source src={target} />
                  {children}
                </video>
              );
            }
            return (
              <a href={href} target="_blank" rel="noreferrer" {...props}>
                {children}
              </a>
            );
          },
          p(props) {
            return <p dir="auto" {...props} />;
          },
        }}
      >
        {normalizedContent}
      </ReactMarkdown>
    </div>
  );
}
