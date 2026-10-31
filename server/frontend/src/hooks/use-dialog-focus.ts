import { useEffect, useRef } from 'react';

// Controlled dialogs may open without a Trigger. Remember outside focus before
// React's autoFocus moves it into a newly mounted input.
export function useDialogFocus() {
  const opener = useRef<HTMLElement | null>(null);
  const rememberOutsideFocus = () => {
    const element = document.activeElement;
    if (element instanceof HTMLElement && element !== document.body &&
        !element.closest('[role="dialog"], [role="alertdialog"]')) {
      opener.current = element;
    }
  };
  useEffect(() => {
    rememberOutsideFocus();
    document.addEventListener('focusin', rememberOutsideFocus);
    return () => document.removeEventListener('focusin', rememberOutsideFocus);
  }, []);
  return {
    onOpenAutoFocus: rememberOutsideFocus,
    onCloseAutoFocus: (event: Event) => {
      event.preventDefault();
      if (opener.current?.isConnected) opener.current.focus();
      else document.getElementById('main-content')?.focus();
    },
  };
}
