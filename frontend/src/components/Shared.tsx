import { useState, type ReactNode } from 'react';
import { Check, Copy, LoaderCircle } from 'lucide-react';
import { toast } from 'sonner';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger,
} from '@/components/ui/alert-dialog';
import { cn, errText } from '@/lib/utils';

const TONES = {
  ok: 'border-transparent bg-emerald-500/15 text-emerald-700 dark:text-emerald-400',
  warn: 'border-transparent bg-amber-500/15 text-amber-700 dark:text-amber-400',
  run: 'border-transparent bg-sky-500/15 text-sky-700 dark:text-sky-400',
  bad: 'border-transparent bg-red-500/15 text-red-700 dark:text-red-400',
  muted: 'border-transparent bg-muted text-muted-foreground',
};
export function StatusBadge({ tone, children }: { tone: keyof typeof TONES; children: ReactNode }) {
  return <Badge variant="outline" className={cn('font-normal', TONES[tone])}>{children}</Badge>;
}

export function Loading() {
  return <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />加载中…</div>;
}
export function Empty({ children }: { children: ReactNode }) {
  return <div className="rounded-lg border border-dashed py-8 text-center text-sm text-muted-foreground">{children}</div>;
}
export function ErrorState({ error }: { error: unknown }) {
  return <p className="text-sm text-destructive">{errText(error)}</p>;
}

// 一段可复制的命令(安装命令、登录链接)。
export function CopyBlock({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  const copy = () => navigator.clipboard.writeText(text).then(
    () => { setDone(true); setTimeout(() => setDone(false), 1500); },
    () => toast.error('复制失败，请手动选中'),
  );
  return <div className="relative">
    <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-md border bg-muted p-3 pr-12 font-mono text-xs">{text}</pre>
    <Button size="icon" variant="ghost" className="absolute right-1 top-1 size-8" onClick={copy} aria-label="复制">
      {done ? <Check /> : <Copy />}
    </Button>
  </div>;
}

// 破坏性操作的确认框。
export function Confirm({ title, description, action = '确定', onConfirm, children }: {
  title: string; description?: string; action?: string; onConfirm: () => void; children: ReactNode;
}) {
  return <AlertDialog>
    <AlertDialogTrigger asChild>{children}</AlertDialogTrigger>
    <AlertDialogContent>
      <AlertDialogHeader><AlertDialogTitle>{title}</AlertDialogTitle>{description && <AlertDialogDescription>{description}</AlertDialogDescription>}</AlertDialogHeader>
      <AlertDialogFooter><AlertDialogCancel>取消</AlertDialogCancel><AlertDialogAction onClick={onConfirm}>{action}</AlertDialogAction></AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>;
}
