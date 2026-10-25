import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Languages, LoaderCircle } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ErrorState } from '@/components/Shared';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { getLocale, setLocale, useT } from '@/lib/i18n';

export function Login() {
  const t = useT();
  const [token, setToken] = useState('');
  // /login?token=… 的登录链接失效时，服务端跳回 /?login=invalid。
  const invalidLink = new URLSearchParams(location.search).get('login') === 'invalid';
  const login = useMutation({
    mutationFn: () => api('/api/login', { method: 'POST', body: { token: token.trim() } }),
    onSuccess: () => { history.replaceState(null, '', '/'); void queryClient.invalidateQueries({ queryKey: ['me'] }); },
    onError: () => {},
  });
  const ready = !!token.trim() && !login.isPending;
  return <div className="relative flex min-h-dvh items-center justify-center bg-muted/40 p-4">
    <Button variant="ghost" size="sm" className="absolute right-4 top-4" onClick={() => setLocale(getLocale() === 'zh' ? 'en' : 'zh')}><Languages />{t('nav.language')}</Button>
    <Card className="w-full max-w-sm">
      <CardHeader>
        <CardTitle className="text-xl">{t('login.title')}</CardTitle>
        <CardDescription>{t('login.text')}</CardDescription>
      </CardHeader>
      <CardContent>
        <form className="grid gap-4" onSubmit={e => { e.preventDefault(); if (ready) login.mutate(); }}>
          <div className="grid gap-2">
            <Label htmlFor="login-token">{t('login.token')}</Label>
            <Input id="login-token" placeholder="ut_..." value={token} onChange={e => setToken(e.target.value)} autoComplete="off" autoFocus />
          </div>
          {login.isError ? <ErrorState error={login.error} /> : invalidLink && <p className="text-sm text-destructive">{t('login.invalidLink')}</p>}
          <Button type="submit" className="w-full" disabled={!ready}>{login.isPending && <LoaderCircle className="animate-spin" />}{t('login.submit')}</Button>
        </form>
      </CardContent>
    </Card>
  </div>;
}
