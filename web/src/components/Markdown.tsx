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
}
