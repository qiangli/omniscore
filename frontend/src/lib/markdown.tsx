import katex from "katex";
import type { ReactNode } from "react";

// Renders a tiny subset of Markdown: paragraphs (blank-line separated),
// **bold**, *italic*, `> blockquote`, and `$...$` KaTeX inline math.
// This is intentionally minimal — it's the exam content's authoring contract.
export function MD({ text }: { text: string }): ReactNode {
  const blocks = text.split(/\n\s*\n/);
  return (
    <>
      {blocks.map((block, i) => {
        const trimmed = block.trim();
        if (trimmed.startsWith("> ")) {
          return (
            <blockquote
              key={i}
              className="border-l-4 border-ruled pl-4 italic my-3 text-ink/80"
            >
              {renderInline(trimmed.replace(/^>\s*/, ""))}
            </blockquote>
          );
        }
        return (
          <p key={i} className="my-3 leading-relaxed">
            {renderInline(trimmed)}
          </p>
        );
      })}
    </>
  );
}

// Inline variant: no paragraph wrapping. Used for stems + choices.
export function MDInline({ text }: { text: string }): ReactNode {
  return <>{renderInline(text)}</>;
}

function renderInline(text: string): ReactNode {
  // Tokenize: math first, then bold, then italic.
  const tokens: { type: "text" | "math" | "bold" | "italic"; v: string }[] = [];
  let i = 0;
  while (i < text.length) {
    const ch = text[i];
    if (ch === "$") {
      const end = text.indexOf("$", i + 1);
      if (end !== -1) {
        tokens.push({ type: "math", v: text.slice(i + 1, end) });
        i = end + 1;
        continue;
      }
    }
    if (ch === "*" && text[i + 1] === "*") {
      const end = text.indexOf("**", i + 2);
      if (end !== -1) {
        tokens.push({ type: "bold", v: text.slice(i + 2, end) });
        i = end + 2;
        continue;
      }
    }
    if (ch === "*") {
      const end = text.indexOf("*", i + 1);
      if (end !== -1 && text[end + 1] !== "*") {
        tokens.push({ type: "italic", v: text.slice(i + 1, end) });
        i = end + 1;
        continue;
      }
    }
    // Accumulate plain text up to next special char.
    let j = i + 1;
    while (j < text.length && text[j] !== "$" && text[j] !== "*") j++;
    tokens.push({ type: "text", v: text.slice(i, j) });
    i = j;
  }
  return tokens.map((t, k) => {
    switch (t.type) {
      case "math":
        return (
          <span
            key={k}
            dangerouslySetInnerHTML={{
              __html: katex.renderToString(t.v, {
                throwOnError: false,
                displayMode: false,
              }),
            }}
          />
        );
      case "bold":
        return (
          <strong key={k} className="font-semibold">
            {renderInline(t.v)}
          </strong>
        );
      case "italic":
        return <em key={k}>{renderInline(t.v)}</em>;
      default:
        return <span key={k}>{t.v}</span>;
    }
  });
}
