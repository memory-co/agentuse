import { useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { useMutation } from '@tanstack/react-query';
import { Pencil } from 'lucide-react';
import { ErrorState } from '@/components/Shared';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import { queryClient } from '@/lib/query';
import type { Conversation } from '@/lib/types';

export function ConversationRename({ conversation }: { conversation: Conversation }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState('');
  const rename = useMutation({
    mutationFn: (title: string) => api(`/api/conversations/${encodeURIComponent(conversation.id)}`, {
      method: 'PATCH',
      body: { title },
    }),
    onSuccess: () => {
      setOpen(false);
      void queryClient.invalidateQueries({ queryKey: ['conversations'] });
    },
  });
  const name = title.trim();
  const canSave = !!name && name !== conversation.title && !rename.isPending;
  const label = t('conversation.rename');

  return <Dialog.Root open={open} onOpenChange={next => {
    if (rename.isPending) return;
    if (next) { setTitle(conversation.title); rename.reset(); }
    setOpen(next);
  }}>
    <Dialog.Trigger asChild>
      <Button variant="ghost" size="icon" className="size-8 shrink-0 text-muted-foreground" aria-label={label} title={label}>
        <Pencil />
      </Button>
    </Dialog.Trigger>
    <Dialog.Portal>
      <Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
      <Dialog.Content className="fixed left-1/2 top-1/2 z-50 w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 rounded-xl border bg-background p-5 shadow-lg" aria-describedby={undefined}>
        <Dialog.Title className="font-semibold">{label}</Dialog.Title>
        <form className="mt-4 grid gap-4" onSubmit={event => { event.preventDefault(); if (canSave) rename.mutate(name); }}>
          <label className="grid gap-1.5 text-sm">
            {t('conversation.name')}
            <Input required value={title} onChange={event => setTitle(event.target.value)} disabled={rename.isPending} />
          </label>
          {rename.isError && <ErrorState error={rename.error} />}
          <div className="flex justify-end gap-2">
            <Dialog.Close asChild><Button type="button" variant="outline" disabled={rename.isPending}>{t('common.cancel')}</Button></Dialog.Close>
            <Button type="submit" disabled={!canSave}>{t('common.save')}</Button>
          </div>
        </form>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}
