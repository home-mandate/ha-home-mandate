// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ApiError, createHttpClient } from './client.ts';
import { sessionFixture } from './fixtures.ts';

interface Call {
  url: string;
  init: RequestInit;
}

const BASE = 'https://ha.example/api/hassio_ingress/f00d/';

/** fakeFetch answers each request with the next response and records the calls. */
function fakeFetch(...responses: (Response | Error | DOMException)[]) {
  const calls: Call[] = [];
  const fetch = async (url: string, init: RequestInit = {}): Promise<Response> => {
    calls.push({ url, init });
    const next = responses.shift();
    if (!next) throw new Error('unexpected request');
    if (!(next instanceof Response)) throw next;
    return next;
  };
  return { fetch, calls };
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const header = (call: Call | undefined, name: string) => new Headers(call?.init.headers).get(name);

/** signedIn returns a client whose session is loaded, plus the recorded calls after it. */
async function signedIn(...responses: (Response | Error | DOMException)[]) {
  const { fetch, calls } = fakeFetch(json(sessionFixture), ...responses);
  const api = createHttpClient({ fetch, base: BASE });
  await api.session();
  return { api, calls: () => calls.slice(1) };
}

const draft = {
  rules: [],
  approval: { timeout: 'PT2M', approvers: ['u1'] },
  limits: { max_actions_per_hour: 60 },
  valid_from: '2026-10-01T00:00:00Z',
};

describe('createHttpClient', () => {
  it('resolves paths against the base, keeping the Ingress prefix', async () => {
    const { fetch, calls } = fakeFetch(json(sessionFixture));
    await createHttpClient({ fetch, base: BASE }).session();
    expect(calls[0]?.url).toBe(`${BASE}api/session`);
  });

  it('resolves against the directory of a page URL', async () => {
    const { fetch, calls } = fakeFetch(json(sessionFixture));
    await createHttpClient({ fetch, base: `${BASE}index.html#/agents` }).session();
    expect(calls[0]?.url).toBe(`${BASE}api/session`);
  });

  it('sends no CSRF header on reads and the session token on writes', async () => {
    const { api, calls } = await signedIn(json({ active: true }));
    await api.setEmergencyStop(true);
    const [put] = calls();
    expect(put?.init.method).toBe('PUT');
    expect(header(put, 'X-HM-CSRF')).toBe(sessionFixture.csrf_token);
    expect(header(put, 'Content-Type')).toBe('application/json');
    expect(put?.init.body).toBe('{"active":true}');
  });

  it('sends reads without a CSRF header', async () => {
    const { fetch, calls } = fakeFetch(json(sessionFixture));
    await createHttpClient({ fetch, base: BASE }).session();
    expect(header(calls[0], 'X-HM-CSRF')).toBeNull();
  });

  it('refuses to write before the session was loaded', async () => {
    const { fetch, calls } = fakeFetch();
    const api = createHttpClient({ fetch, base: BASE });
    await expect(api.setEmergencyStop(true)).rejects.toMatchObject({ code: 'csrf_invalid' });
    expect(calls).toHaveLength(0);
  });

  it('rejects a session without a CSRF token', async () => {
    const { fetch } = fakeFetch(json({ ...sessionFixture, csrf_token: 42 }));
    await expect(createHttpClient({ fetch, base: BASE }).session()).rejects.toMatchObject({ code: 'internal' });
  });

  it('takes a new CSRF token from the session returned by setLanguage', async () => {
    const { api, calls } = await signedIn(json({ ...sessionFixture, csrf_token: 'rotated' }), json({ active: false }));
    await api.setLanguage('de');
    await api.setEmergencyStop(false);
    expect(header(calls()[1], 'X-HM-CSRF')).toBe('rotated');
  });

  it('stays on the same origin, follows no redirects and caches nothing', async () => {
    const { fetch, calls } = fakeFetch(json(sessionFixture));
    await createHttpClient({ fetch, base: BASE }).session();
    expect(calls[0]?.init).toMatchObject({ credentials: 'same-origin', redirect: 'error', cache: 'no-store' });
  });

  it('gives every request a timeout signal', async () => {
    const { fetch, calls } = fakeFetch(json(sessionFixture));
    await createHttpClient({ fetch, base: BASE }).session();
    expect(calls[0]?.init.signal).toBeInstanceOf(AbortSignal);
  });

  it('reports a timeout as "unavailable"', async () => {
    const { fetch } = fakeFetch(new DOMException('timed out', 'TimeoutError'));
    await expect(createHttpClient({ fetch, base: BASE }).session()).rejects.toMatchObject({ code: 'unavailable', status: 0 });
  });

  it('sends client IDs in the body, never in the path', async () => {
    const { api, calls } = await signedIn(json({}));
    await api.revokeAgent('https://agent.example/client?x=1#y');
    const [post] = calls();
    expect(post?.url).toBe(`${BASE}api/agents/revoke`);
    expect(post?.init.body).toBe('{"client_id":"https://agent.example/client?x=1#y"}');
  });

  it('encodes path segments', async () => {
    const { api, calls } = await signedIn(json({}));
    await api.mandateVersion('m1', 'sha256:ab/c');
    expect(calls()[0]?.url).toBe(`${BASE}api/mandates/m1/versions/sha256%3Aab%2Fc`);
  });

  it('rejects identifiers that would change the path', async () => {
    const { api } = await signedIn();
    await expect(api.template('..')).rejects.toMatchObject({ code: 'invalid_input' });
    await expect(api.template('.')).rejects.toMatchObject({ code: 'invalid_input' });
    await expect(api.template('')).rejects.toMatchObject({ code: 'invalid_input' });
  });

  it('builds the audit query string from set filters only', async () => {
    const { fetch, calls } = fakeFetch(json({ entries: [], next_before: null }));
    await createHttpClient({ fetch, base: BASE }).audit({
      before: 120,
      limit: 50,
      decision: 'deny',
      agent: 'pair:a b',
      entity_id: 'lock.front_door',
      event: 'decision',
    });
    expect(calls[0]?.url).toBe(
      `${BASE}api/audit?before=120&limit=50&agent=pair%3Aa+b&entity_id=lock.front_door&event=decision&decision=deny`,
    );
  });

  it('asks for the newest audit entries without a query', async () => {
    const { fetch, calls } = fakeFetch(json({ entries: [], next_before: null }));
    await createHttpClient({ fetch, base: BASE }).audit({});
    expect(calls[0]?.url).toBe(`${BASE}api/audit`);
  });

  it('accepts 204 without a body', async () => {
    const { api } = await signedIn(new Response(null, { status: 204 }));
    await expect(api.deleteTemplate('voice')).resolves.toBeUndefined();
  });

  it('maps an error body to ApiError with code and field', async () => {
    const { api } = await signedIn(json({ code: 'invalid_mandate', field: '/draft/rules/0/actions' }, 422));
    const err = await api.putTemplate('voice', { draft }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ code: 'invalid_mandate', status: 422, field: '/draft/rules/0/actions' });
  });

  it.each([
    [401, 'unauthenticated'],
    [403, 'forbidden'],
    [404, 'not_found'],
    [409, 'conflict'],
    [413, 'too_large'],
    [429, 'rate_limited'],
    [500, 'internal'],
    [502, 'unavailable'],
    [503, 'unavailable'],
    [504, 'unavailable'],
    [418, 'internal'],
  ])('maps status %i without a usable body to %s', async (status, code) => {
    const { fetch } = fakeFetch(new Response('<html>proxy page</html>', { status }));
    await expect(createHttpClient({ fetch, base: BASE }).session()).rejects.toMatchObject({ code, status });
  });

  it('does not trust an unknown error code from the body', async () => {
    const { fetch } = fakeFetch(json({ code: '<script>' }, 400));
    await expect(createHttpClient({ fetch, base: BASE }).session()).rejects.toMatchObject({ code: 'invalid_input' });
  });

  it('ignores a field that is not a JSON pointer', async () => {
    const { fetch } = fakeFetch(json({ code: 'invalid_input', field: 'javascript:alert(1)' }, 400));
    await expect(createHttpClient({ fetch, base: BASE }).session()).rejects.toMatchObject({
      code: 'invalid_input',
      field: undefined,
    });
  });

  it('reports a network failure as "unavailable"', async () => {
    const { fetch } = fakeFetch(new TypeError('Failed to fetch'));
    await expect(createHttpClient({ fetch, base: BASE }).session()).rejects.toMatchObject({ code: 'unavailable', status: 0 });
  });

  it('rejects a successful answer that is not JSON', async () => {
    const { fetch } = fakeFetch(new Response('<html>', { status: 200, headers: { 'Content-Type': 'text/html' } }));
    await expect(createHttpClient({ fetch, base: BASE }).session()).rejects.toMatchObject({ code: 'internal' });
  });

  it('rejects a successful answer with broken JSON as ApiError', async () => {
    const { fetch } = fakeFetch(new Response('{"user":', { status: 200, headers: { 'Content-Type': 'application/json' } }));
    const err = await createHttpClient({ fetch, base: BASE }).session().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ code: 'internal', status: 200 });
  });

  it('forgets the CSRF token after csrf_invalid until the session is reloaded', async () => {
    const { api, calls } = await signedIn(
      json({ code: 'csrf_invalid' }, 403),
      json({ ...sessionFixture, csrf_token: 'fresh' }),
      json({ active: false }),
    );
    await expect(api.setEmergencyStop(false)).rejects.toMatchObject({ code: 'csrf_invalid' });
    await expect(api.setEmergencyStop(false)).rejects.toMatchObject({ code: 'csrf_invalid' });
    expect(calls()).toHaveLength(1);
    await api.session();
    await expect(api.setEmergencyStop(false)).resolves.toEqual({ active: false });
    expect(header(calls()[2], 'X-HM-CSRF')).toBe('fresh');
  });

  it('calls every endpoint with the method, path and body of the contract', async () => {
    const answers = Array.from({ length: 24 }, () => json({ ...sessionFixture }));
    const { api: c, calls } = await signedIn(...answers);
    await c.setLanguage('de');
    await c.agents();
    await c.revokeAgent('pair:kitchen');
    await c.pairing();
    await c.devices();
    await c.mandates();
    await c.createMandate({ client_id: 'pair:kitchen', template: 'voice' });
    await c.mandate('m1');
    await c.mandateVersion('m1', 'sha256:ab');
    await c.putMandate('m1', { draft, base_digest: 'sha256:ab', confirm_critical: true });
    await c.revokeMandate('m1');
    await c.preview({ draft, mandate_id: 'm1' });
    await c.templates();
    await c.template('voice');
    await c.putTemplate('voice', { draft });
    await c.deleteTemplate('voice');
    await c.audit({});
    await c.verifyAudit();
    await c.approvers();
    await c.putApprover('u1', { notify_service: 'mobile_app_a', language: null });
    await c.testApprover('u1');
    await c.deleteApprover('u1');
    await c.emergencyStop();
    await c.setEmergencyStop(false);
    expect(calls().map((x) => `${x.init.method} ${x.url.slice(BASE.length)} ${x.init.body ?? ''}`.trim())).toEqual([
      'PUT api/session/language {"language":"de"}',
      'GET api/agents',
      'POST api/agents/revoke {"client_id":"pair:kitchen"}',
      'GET api/pairing',
      'GET api/devices',
      'GET api/mandates',
      'POST api/mandates {"client_id":"pair:kitchen","template":"voice"}',
      'GET api/mandates/m1',
      'GET api/mandates/m1/versions/sha256%3Aab',
      `PUT api/mandates/m1 ${JSON.stringify({ draft, base_digest: 'sha256:ab', confirm_critical: true })}`,
      'POST api/mandates/m1/revoke',
      `POST api/mandates/preview ${JSON.stringify({ draft, mandate_id: 'm1' })}`,
      'GET api/templates',
      'GET api/templates/voice',
      `PUT api/templates/voice ${JSON.stringify({ draft })}`,
      'DELETE api/templates/voice',
      'GET api/audit',
      'GET api/audit/verify',
      'GET api/approvers',
      'PUT api/approvers/u1 {"notify_service":"mobile_app_a","language":null}',
      'POST api/approvers/u1/test',
      'DELETE api/approvers/u1',
      'GET api/emergency-stop',
      'PUT api/emergency-stop {"active":false}',
    ]);
  });
});
