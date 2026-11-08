import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { cn } from '@/lib/utils';

export function Markdown({ children, className }: { children: string; className?: string }) {
  return <div className={cn('markdown min-w-0 text-sm leading-7 [overflow-wrap:anywhere]', className)} data-markdown>
    <ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml components={{
      a: ({ node: _node, ...props }) => <a {...props} target="_blank" rel="noopener noreferrer" />,
      table: ({ node: _node, ...props }) => <div className="overflow-x-auto"><table {...props} /></div>,
    }}>{children}</ReactMarkdown>
  </div>;
}
