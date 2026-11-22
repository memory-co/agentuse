import { api, apiData } from './client';

// TypeScript checks this file during build. It is independent of the browser bundle.
export function checkMessageContract() {
  const path = '/api/conversations/{id}/messages';
  const params = { path: { id: 'conversation' } };
  const reply = apiData(api.POST(path, { params, body: { prompt: 'hello', to_agent_ids: ['agent'] } }));
  void reply.then(data => data.message?.content);

  // @ts-expect-error The request uses to_agent_ids even when other fields are present.
  void api.POST(path, { params, body: { prompt: 'hello', recipient_ids: ['agent'] } });
  // @ts-expect-error The route requires its conversation ID.
  void api.POST(path, { body: { prompt: 'hello' } });
  // @ts-expect-error The round query parameter is an integer.
  void api.GET('/api/conversations/{id}', { params: { ...params, query: { round: '1' } } });
  // @ts-expect-error The reply has a message, not an output field.
  void reply.then(data => data.output);
}
