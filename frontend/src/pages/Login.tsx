import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { LoaderCircle } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ErrorState } from '@/components/Shared';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';

export function Login() {
  const [token, setToken] = useState('');
  // /login?token=… 的登录链接失效时，服务端跳回 /?login=invalid。
  const invalidLink = new URLSearchParams(location.search).get('login') === 'invalid';
  const login = useMutation({
    mutationFn: () => api('/api/login', { method: 'POST', body: { token: token.trim() } }),
    onSuccess: () => { history.replaceState(null, '', '/'); void queryClient.invalidateQueries({ queryKey: ['me'] }); },
    onError: () => {},
  });
  const ready = !!token.trim() && !login.isPending;
  return <div className="flex min-h-dvh items-center justify-center bg-muted/40 p-4">
    <Card className="w-full max-w-sm">
      <CardHeader>
        <CardTitle className="text-xl">登录 agentuse</CardTitle>
        <CardDescription>输入登录令牌(ut_ 开头)。管理员令牌在服务第一次启动时打印在日志里；其他用户的令牌由管理员创建。</CardDescription>
      </CardHeader>
      <CardContent>
        <form className="grid gap-4" onSubmit={e => { e.preventDefault(); if (ready) login.mutate(); }}>
          <div className="grid gap-2">
            <Label htmlFor="login-token">登录令牌</Label>
            <Input id="login-token" placeholder="ut_..." value={token} onChange={e => setToken(e.target.value)} autoComplete="off" autoFocus />
          </div>
          {login.isError ? <ErrorState error={login.error} /> : invalidLink && <p className="text-sm text-destructive">登录链接无效</p>}
          <Button type="submit" className="w-full" disabled={!ready}>{login.isPending && <LoaderCircle className="animate-spin" />}登录</Button>
        </form>
      </CardContent>
    </Card>
  </div>;
}
