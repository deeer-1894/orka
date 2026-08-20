import { useEffect, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import rehypeKatex from "rehype-katex";
import "katex/dist/katex.min.css";
import { salesBIAssets } from "../api";
import { parseSalesBIAssetURL, type SalesBIAsset } from "../lib/salesBIAssets";
import { Icon } from "./Icon";

// Models emit math in several delimiter styles. remark-math understands $…$ and
// $$…$$; normalize the LaTeX-native \(…\) and \[…\] forms to those so all of them
// render via KaTeX. Done as a string pre-pass — the simplest place that covers
// every model.
function normalizeMath(s: string): string {
  return s
    .replace(/\\\[([\s\S]+?)\\\]/g, (_m, body) => `$$${body}$$`)
    .replace(/\\\(([\s\S]+?)\\\)/g, (_m, body) => `$${body}$`);
}

function SalesBIChart({ asset, alt }: { asset: SalesBIAsset; alt: string }) {
  return (
    <figure className="group/bi relative my-4 w-full overflow-hidden rounded-lg border border-border bg-white">
      <img
        src={salesBIAssets.previewURL(asset)}
        alt={alt}
        loading="lazy"
        decoding="async"
        className="block max-h-[34rem] w-full object-contain"
      />
      <a
        href={salesBIAssets.downloadURL(asset)}
        className="absolute right-2 top-2 grid h-8 w-8 place-items-center rounded-md border border-border bg-surface/95 text-muted opacity-0 shadow-sm transition hover:text-accent focus:opacity-100 group-hover/bi:opacity-100"
        aria-label="下载图表"
        title="下载图表"
      >
        <Icon name="download" size={15} />
      </a>
    </figure>
  );
}

function SalesBIReportLink({ asset, children }: { asset: SalesBIAsset; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!open) return;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  return (
    <>
      <span className="inline-flex items-center gap-1.5 align-middle">
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="inline-flex items-center gap-1 text-accent underline underline-offset-2"
        >
          <Icon name="eye" size={14} />
          <span>{children}</span>
        </button>
        <a
          href={salesBIAssets.downloadURL(asset)}
          className="inline-grid h-6 w-6 place-items-center rounded-md text-muted no-underline hover:bg-surface2 hover:text-accent"
          aria-label="下载报告"
          title="下载报告"
        >
          <Icon name="download" size={14} />
        </a>
      </span>
      {open && createPortal(
        <div className="fixed inset-0 z-[80] flex items-center justify-center bg-black/45 p-3 sm:p-6" onClick={() => setOpen(false)}>
          <section
            role="dialog"
            aria-modal="true"
            aria-label="Sales BI 报告预览"
            className="flex h-[92vh] w-full max-w-[1280px] flex-col overflow-hidden rounded-lg border border-border bg-surface shadow-2xl"
            onClick={(event) => event.stopPropagation()}
          >
            <header className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-3 sm:px-4">
              <Icon name="chart" size={16} className="text-accent" />
              <span className="min-w-0 flex-1 truncate text-[13px] font-medium text-ink">{asset.filename}</span>
              <a
                href={salesBIAssets.downloadURL(asset)}
                className="inline-flex h-8 items-center gap-1.5 rounded-md px-2 text-[12px] text-muted no-underline hover:bg-surface2 hover:text-accent"
                title="下载报告"
              >
                <Icon name="download" size={14} />
                <span>下载</span>
              </a>
              <button
                type="button"
                onClick={() => setOpen(false)}
                className="grid h-8 w-8 place-items-center rounded-md text-muted hover:bg-surface2 hover:text-ink"
                aria-label="关闭报告预览"
                title="关闭"
              >
                <Icon name="close" size={16} />
              </button>
            </header>
            <iframe
              src={salesBIAssets.previewURL(asset)}
              title={asset.filename}
              sandbox="allow-scripts"
              referrerPolicy="no-referrer"
              className="min-h-0 flex-1 border-0 bg-white"
            />
          </section>
        </div>,
        document.body,
      )}
    </>
  );
}

export function Markdown({
  children,
  resolveImage,
}: {
  children: string;
  // Optional rewriter for image sources. Markdown image paths are usually
  // relative to the document (e.g. ![](chart.png)); without this they resolve
  // against the page origin and 404. FilePreview passes a resolver that maps
  // them to the workspace file API.
  resolveImage?: (src: string) => string;
}) {
  return (
    <div className="md">
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkMath]}
        rehypePlugins={[[rehypeKatex, { throwOnError: false, errorColor: "var(--color-accent)" }]]}
        components={{
          a: ({ href, children: linkChildren, ...props }) => {
            const asset = typeof href === "string" ? parseSalesBIAssetURL(href) : null;
            if (asset?.kind === "report") {
              return <SalesBIReportLink asset={asset}>{linkChildren}</SalesBIReportLink>;
            }
            return (
              <a
                {...props}
                href={asset ? salesBIAssets.previewURL(asset) : href}
                target="_blank"
                rel="noreferrer"
              >
                {linkChildren}
              </a>
            );
          },
          img: ({ src, alt, ...props }) => {
            const asset = typeof src === "string" ? parseSalesBIAssetURL(src) : null;
            if (asset?.kind === "chart") {
              return <SalesBIChart asset={asset} alt={alt || "Sales BI 图表"} />;
            }
            return (
              <img
                {...props}
                alt={alt}
                src={resolveImage && typeof src === "string" ? resolveImage(src) : src}
              />
            );
          },
        }}
      >
        {normalizeMath(children)}
      </ReactMarkdown>
    </div>
  );
}
