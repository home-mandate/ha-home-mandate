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
    await api.template('guest room/a b');
    expect(calls()[0]?.url).toBe(`${BASE}api/templates/guest%20room%2Fa%20b`);
  });

  it('asks for a version by its number and refuses anything that is not one', async () => {
    const { api, calls } = await signedIn(json({}));
    await api.mandateVersion('m1', 3);
    expect(calls()[0]?.url).toBe(`${BASE}api/mandates/m1/versions/3`);
    for (const bad of [0, -1, 1.5, NaN, Infinity]) {
      await expect(api.mandateVersion('m1', bad)).rejects.toMatchObject({ code: 'invalid_input' });
    }
    expect(calls()).toHaveLength(1);
  });

  it('rejects identifiers that would change the path', async () => {
    const { api } = await signedIn();
    await expect(api.template('..')).rejects.toMatchObject({ code: 'invalid_input' });
    await expect(api.template('.')).rejects.toMatchObject({ code: 'invalid_input' });
    await expect(api.template('')).rejects.toMatchObject({ code: 'invalid_input' });
  });

  it('builds the audit query string from set filters only, one parameter per decision', async () => {
    const { fetch, calls } = fakeFetch(json({ entries: [], next_before: null, total: 0 }));
    await createHttpClient({ fetch, base: BASE }).audit({
      before: 120,
      limit: 50,
      since: '2026-10-01T00:00:00Z',
      until: '2026-10-02T00:00:00Z',
      agent: 'pair:a b',
      device: 'lock.front_door',
      q: 'Haus tür&x',
      group: 'decision',
      event: 'decision',
      decisions: ['deny', 'default'],
    });
    expect(calls[0]?.url).toBe(
      `${BASE}api/audit?before=120&limit=50&since=2026-10-01T00%3A00%3A00Z&until=2026-10-02T00%3A00%3A00Z` +
        '&agent=pair%3Aa+b&device=lock.front_door&q=Haus+t%C3%BCr%26x&group=decision&event=decision&decision=deny&decision=default',
    );
  });

  it('asks for the newest audit entries without a query', async () => {
    const { fetch, calls } = fakeFetch(json({ entries: [], next_before: null, total: 0 }));
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
    [410, 'internal'],
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

  it('passes retry_after of a lock on as seconds', async () => {
    const { api } = await signedIn(json({ code: 'pairing_locked', retry_after: 540 }, 429));
    await expect(api.pairingCheck('BCDF-GHJK')).rejects.toMatchObject({ code: 'pairing_locked', retryAfter: 540 });
  });

  it('maps a bare 410 to an expired code only for pairing calls', async () => {
    const { api } = await signedIn(new Response('gone', { status: 410 }));
    await expect(api.pairingCheck('BCDF-GHJK')).rejects.toMatchObject({ code: 'pairing_code_expired' });
  });

  it('falls back to the Retry-After header of a proxy', async () => {
    const { fetch } = fakeFetch(new Response('busy', { status: 429, headers: { 'Retry-After': '30' } }));
    await expect(createHttpClient({ fetch, base: BASE }).system()).rejects.toMatchObject({ code: 'rate_limited', retryAfter: 30 });
  });

  it('ignores a retry_after that is not a small positive integer', async () => {
    const { api } = await signedIn(json({ code: 'rate_limited', retry_after: -1 }, 429), json({ code: 'rate_limited', retry_after: 1e9 }, 429));
    await expect(api.system()).rejects.toMatchObject({ retryAfter: undefined });
    await expect(api.system()).rejects.toMatchObject({ retryAfter: undefined });
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

  // A rotated token must not block writes until a reload, least of all the emergency stop (security S2).
  it('fetches a fresh session after csrf_invalid and repeats the write once', async () => {
    const { api, calls } = await signedIn(
      json({ code: 'csrf_invalid' }, 403),
      json({ ...sessionFixture, csrf_token: 'fresh' }),
      json({ active: true }),
    );
    await expect(api.setEmergencyStop(true)).resolves.toEqual({ active: true });
    expect(calls().map((c) => c.init.method ?? 'GET')).toEqual(['PUT', 'GET', 'PUT']);
    expect(header(calls()[2], 'X-HM-CSRF')).toBe('fresh');
  });

  it('gives up after one repeat that is refused again', async () => {
    const { api, calls } = await signedIn(
      json({ code: 'csrf_invalid' }, 403),
      json({ ...sessionFixture, csrf_token: 'fresh' }),
      json({ code: 'csrf_invalid' }, 403),
    );
    await expect(api.setEmergencyStop(true)).rejects.toMatchObject({ code: 'csrf_invalid', status: 403 });
    expect(calls()).toHaveLength(3);
  });

  it('reports csrf_invalid when no fresh session can be fetched', async () => {
    const { api } = await signedIn(json({ code: 'csrf_invalid' }, 403), new TypeError('offline'));
    await expect(api.setEmergencyStop(true)).rejects.toMatchObject({ code: 'csrf_invalid', status: 403 });
  });

  it('calls every endpoint with the method, path and body of the contract', async () => {
    const answers = Array.from({ length: 30 }, () => json({ ...sessionFixture }));
    const { api: c, calls } = await signedIn(...answers);
    await c.setLanguage('de');
    await c.system();
    await c.agents();
    await c.revokeAgent('pair:kitchen');
    await c.pairingCheck('bcdf-ghjk');
    await c.pairingApprove({ code: 'BCDFGHJK', pairing_id: 'pg-1', display_name: 'Küche', template: 'voice' });
    await c.pairingDeny({ code: 'BCDFGHJK', pairing_id: 'pg-1' });
    await c.devices();
    await c.mandates();
    await c.createMandate({ client_id: 'pair:kitchen', template: 'voice' });
    await c.mandate('m1');
    await c.mandateVersion('m1', 2);
    await c.putMandate('m1', { name: 'Küche', draft, base_digest: 'sha256:ab', confirm_critical: true });
    await c.applyTemplate('m1', { template: 'voice', base_digest: 'sha256:ab' });
    await c.revokeMandate('m1');
    await c.templates();
    await c.template('voice');
    await c.putTemplate('voice', { draft });
    await c.deleteTemplate('voice');
    await c.settings();
    await c.putSettings({ approval_timeout: 'PT2M', max_actions_per_hour: 60, bell: false });
    await c.approvals();
    await c.answerApproval('apr 1', true);
    await c.audit({});
    await c.verifyAudit();
    await c.approvers();
    await c.putApprover('u1', { devices: [{ service: 'mobile_app_a', critical: true }], ui: false, ui_critical: false, language: null }, 'v/1');
    await c.testApprover('u1');
    await c.deleteApprover('u1', 'v/1');
    await c.setEmergencyStop(false);
    expect(calls().map((x) => `${x.init.method} ${x.url.slice(BASE.length)} ${x.init.body ?? ''}`.trim())).toEqual([
      'PUT api/session/language {"language":"de"}',
      'GET api/system',
      'GET api/agents',
      'POST api/agents/revoke {"client_id":"pair:kitchen"}',
      'POST api/pairing/check {"code":"bcdf-ghjk"}',
      'POST api/pairing/approve {"code":"BCDFGHJK","pairing_id":"pg-1","display_name":"Küche","template":"voice"}',
      'POST api/pairing/deny {"code":"BCDFGHJK","pairing_id":"pg-1"}',
      'GET api/devices',
      'GET api/mandates',
      'POST api/mandates {"client_id":"pair:kitchen","template":"voice"}',
      'GET api/mandates/m1',
      'GET api/mandates/m1/versions/2',
      `PUT api/mandates/m1 ${JSON.stringify({ name: 'Küche', draft, base_digest: 'sha256:ab', confirm_critical: true })}`,
      'POST api/mandates/m1/apply-template {"template":"voice","base_digest":"sha256:ab"}',
      'POST api/mandates/m1/revoke',
      'GET api/templates',
      'GET api/templates/voice',
      `PUT api/templates/voice ${JSON.stringify({ draft })}`,
      'DELETE api/templates/voice',
      'GET api/settings',
      'PUT api/settings {"approval_timeout":"PT2M","max_actions_per_hour":60,"bell":false}',
      'GET api/approvals',
      'POST api/approvals/apr%201/answer {"approve":true}',
      'GET api/audit',
      'POST api/audit/verify',
      'GET api/approvers',
      'PUT api/approvers/u1 {"devices":[{"service":"mobile_app_a","critical":true}],"ui":false,"ui_critical":false,"language":null,"base_version":"v/1"}',
      'POST api/approvers/u1/test',
      'DELETE api/approvers/u1?base_version=v%2F1',
      'PUT api/emergency-stop {"active":false}',
    ]);
  });

  it('opens the event stream next to the page with the current CSRF token', async () => {
    const sockets: { url: string; sent: string[]; onopen: (() => void) | null; onmessage: ((e: { data: unknown }) => void) | null }[] = [];
    const { fetch } = fakeFetch(json(sessionFixture));
    const api = createHttpClient({
      fetch,
      base: BASE,
      socket: (url) => {
        const s = { url, sent: [] as string[], onopen: null, onmessage: null, onclose: null, send(d: string) { this.sent.push(d); }, close() {} };
        sockets.push(s);
        return s;
      },
    });
    await api.session();
    const states: string[] = [];
    const stop = api.events({ onEvent: () => {}, onState: (st) => states.push(st) });
    sockets[0]?.onopen?.();
    expect(sockets[0]?.url).toBe('wss://ha.example/api/hassio_ingress/f00d/api/events');
    expect(sockets[0]?.sent).toEqual([`{"csrf":"${sessionFixture.csrf_token}"}`]);
    expect(states).toEqual(['connecting']);
    sockets[0]?.onmessage?.({ data: '{"type":"agents.changed"}' });
    expect(states).toEqual(['connecting', 'open']);
    stop.close();
  });
});
