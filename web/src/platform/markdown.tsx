import DOMPurify from "dompurify";
import { marked } from "marked";
import { useMemo, type ReactNode } from "react";

const ALLOWED_TAGS = [
  "p",
  "br",
  "strong",
  "em",
  "code",
  "pre",
  "blockquote",
  "ul",
  "ol",
  "li",
  "a",
];

export function SafeMarkdown({
  text,
  className,
  id,
  tabIndex,
}: {
  text: string;
  className?: string;
  id?: string;
  tabIndex?: number;
}): ReactNode {
  const html = useMemo(
    () => DOMPurify.sanitize(marked.parse(text, { async: false }), {
      ALLOWED_TAGS,
      ALLOWED_ATTR: ["href", "title"],
      ALLOW_ARIA_ATTR: false,
      ALLOW_DATA_ATTR: false,
    }),
    [text],
  );
  return <div id={id} className={className} tabIndex={tabIndex} dangerouslySetInnerHTML={{ __html: html }} />;
}
