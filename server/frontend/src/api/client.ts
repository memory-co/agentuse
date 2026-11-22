import createClient, { type ClientPathsWithMethod, type FetchOptions, type FetchResponse, type MaybeOptionalInit } from 'openapi-fetch';
import { clearCurrentUser } from '@/lib/query';
import { actAsHeaders } from '@/lib/actas';
import type { paths } from '@/api/schema';

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

async function checkResponse(response: Response, path: string) {
  if (response.ok) return;
  if (response.status === 401 && path !== '/api/login' && path !== '/api/me') clearCurrentUser();
  const body: unknown = await response.json().catch(() => null);
  const error = body && typeof body === 'object' && 'error' in body ? body.error : undefined;
  const message = typeof error === 'string' && error ? error : response.statusText;
  throw new ApiError(response.status, message);
}

const transport = createClient<paths>({ baseUrl: location.origin });
transport.use({
  onRequest({ request }) {
    for (const [key, value] of Object.entries(actAsHeaders(new URL(request.url).pathname))) {
      request.headers.set(key, value);
    }
  },
  async onResponse({ response, schemaPath }) { await checkResponse(response, schemaPath); },
});

type SuccessData<Result> = Result extends { data: infer Data } ? Data : never;
type Method = 'get' | 'post' | 'put' | 'patch' | 'delete';
type APIPath<M extends Method> = ClientPathsWithMethod<typeof transport, M>;
type ParseOption<Operation> = Operation extends { responses: { 200: { content: Record<string, Blob> } } }
  ? { parseAs: 'blob' }
  : { parseAs?: never };
type APIResponse<M extends Method, P extends APIPath<M>> = FetchResponse<NonNullable<paths[P][M]>, ParseOption<paths[P][M]>, `${string}/${string}`>;
type APIOptions<M extends Method, P extends APIPath<M>> = Omit<FetchOptions<paths[P][M]>, 'parseAs'> & ParseOption<paths[P][M]>;
type APIArguments<M extends Method, P extends APIPath<M>> = undefined extends MaybeOptionalInit<paths[P], M>
  ? [options?: APIOptions<M, P>]
  : [options: APIOptions<M, P>];
type APIClient = {
  [M in Method as Uppercase<M>]: <P extends APIPath<M>>(path: P, ...args: NoInfer<APIArguments<M, P>>) => Promise<APIResponse<M, P>>;
};

// Infer options from the path alone so excess body and query fields are checked.
export const api = transport as APIClient;

// The middleware throws on error responses before openapi-fetch parses the body.
export async function apiData<Result extends { data?: unknown; error?: unknown }>(request: Promise<Result>): Promise<SuccessData<Result>> {
  const result = await request;
  if ('error' in result) throw result.error;
  return result.data as SuccessData<Result>;
}

export function multipartBody(fields: Record<string, string | Blob | undefined>) {
  const body = new FormData();
  for (const [key, value] of Object.entries(fields)) {
    if (value !== undefined) body.append(key, value);
  }
  return body;
}

export async function fetchFile(path: string, signal?: AbortSignal) {
  const response = await fetch(path, { headers: actAsHeaders(path), signal });
  await checkResponse(response, path);
  return response;
}

export async function downloadFile(path: string, filename: string) {
  saveDownload(await (await fetchFile(path)).blob(), filename);
}

export function saveDownload(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
