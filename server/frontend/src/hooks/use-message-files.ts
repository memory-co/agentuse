import { useRef, useState } from 'react';
import { toast } from 'sonner';
import { uploadAttachment } from '@/lib/attachments';
import { queryClient } from '@/lib/query';
import { useT } from '@/lib/i18n';
import type { Attachment } from '@/lib/types';

export interface DraftFile { key: string; file?: File; attachment?: Attachment; progress?: number }
export function useMessageFiles() {
  const t = useT();
  const [files, setFiles] = useState<DraftFile[]>([]);
  const uploaded = useRef(new Map<string, Attachment>());
  const nextKey = useRef(0);
  const addFiles = (incoming: File[]) => {
    if (files.length + incoming.length > 64) { toast.error(t('attachment.limit')); return; }
    // Draft keys are local UI identities, not persisted attachment IDs.
    const additions = incoming.map(file => ({ key: `draft-${nextKey.current++}`, file }));
    setFiles(current => [...current, ...additions]);
  };
  const addExisting = (attachment: Attachment) => {
    if (files.some(item => item.attachment?.id === attachment.id)) return;
    if (files.length >= 64) { toast.error(t('attachment.limit')); return; }
    setFiles(current => [...current, { key: attachment.id, attachment }]);
  };
  const remove = (key: string) => setFiles(current => current.filter(item => item.key !== key));
  const clear = () => { setFiles([]); uploaded.current.clear(); };
  const upload = async (conversation: string) => {
    const ids: string[] = [];
    try {
      for (const item of files) {
        let attachment = item.attachment || uploaded.current.get(item.key);
        if (!attachment && item.file) {
          attachment = await uploadAttachment(conversation, item.file, progress => setFiles(current => current.map(file => file.key === item.key ? { ...file, progress } : file)));
          uploaded.current.set(item.key, attachment);
        }
        if (attachment) ids.push(attachment.id);
      }
      return ids;
    } finally { void queryClient.invalidateQueries({ queryKey: ['attachments', conversation] }); }
  };
  return { files, addFiles, addExisting, remove, clear, upload };
}
