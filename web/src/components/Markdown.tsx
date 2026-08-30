import { memo } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import rehypeKatex from "rehype-katex";
import "katex/dist/katex.min.css";

// Models emit math in several delimiter styles. remark-math understands $…$ and
// $$…$$; normalize the LaTeX-native \(…\) and \[…\] forms to those so all of them
// render via KaTeX. Done as a string pre-pass — the simplest place that covers
// every model.
function normalizeMath(s: string): string {
  return s
    .replace(/\\\[([\s\S]+?)\\\]/g, (_m, body) => `$$${body}$$`)
    .replace(/\\\(([\s\S]+?)\\\)/g, (_m, body) => `$${body}$`);
}

// Memoized: parsing markdown is the most expensive thing the thread does, and a
// streaming turn re-renders the whole thread on every token batch — which made
// EVERY historical answer re-parse each time (measured: 252 re-parses for one
// turn in a 14-answer conversation). The rendered text only depends on the
// source string (and the image resolver), so identical props can reuse the tree;
// the streaming bubble still updates because its string changes every token.
export const Markdown = memo(function Markdown({
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
          a: (props) => <a {...props} target="_blank" rel="noreferrer" />,
          img: ({ src, ...props }) => (
            <img {...props} src={resolveImage && typeof src === "string" ? resolveImage(src) : src} />
          ),
        }}
      >
        {normalizeMath(children)}
      </ReactMarkdown>
    </div>
  );
});
