import DOMPurify from 'dompurify'
import { marked } from 'marked'
import { useMemo } from 'react'

marked.setOptions({ gfm: true, breaks: true })

export function Markdown({ children }: { children: string }) {
  const safe = useMemo(() => DOMPurify.sanitize(marked.parse(children, { async: false }) as string, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ['style', 'script', 'iframe', 'object', 'embed', 'form'],
    FORBID_ATTR: ['style'],
  }), [children])
  return <div className="markdown" dangerouslySetInnerHTML={{ __html: safe }} />
}

