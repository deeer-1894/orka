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

export function Markdown({ children }: { children: string }) {
  return (
    <div className="md">
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkMath]}
        rehypePlugins={[[rehypeKatex, { throwOnError: false, errorColor: "var(--color-accent)" }]]}
        components={{
          a: (props) => <a {...props} target="_blank" rel="noreferrer" />,
        }}
      >
        {normalizeMath(children)}
      </ReactMarkdown>
    </div>
  );
}
