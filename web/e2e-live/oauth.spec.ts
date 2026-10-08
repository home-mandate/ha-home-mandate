// SPDX-License-Identifier: AGPL-3.0-or-later

// The sign-in, consent and pairing pages of the authorization server in a real browser
// (docs/TESTING.md section 3), against the release image: a human signs in on Home
// Assistant's real login page and decides on the consent page. The spec also plays the
// agent: it reads the metadata, waits on loopback for the redirect (RFC 8252), exchanges
// the code with its PKCE verifier and calls the MCP endpoint. The client metadata
// document comes from tools/cimdserver. Started by e2e/oauth_test.go (make e2e-ui), which
// also checks afterwards who was admitted.
//
// Browsers behave in ways a Go HTTP client does not imitate (an "Origin: null" form post
// under a strict referrer policy refused every consent once), so every step goes through
// Chromium: no request headers are set by hand. Every test fails on a CSP violation.
import { createHash, randomBytes } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import { test as base, expect, type Page } from '@playwright/test';

function env(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is not set; run through e2e/oauth_test.go (make e2e-ui)`);
  return value;
}

const gateway = env('HM_LIVE_PUBLIC');
const clientID = env('HM_LIVE_CLIENT_ID');
const admin = { user: env('HM_LIVE_USER'), password: env('HM_LIVE_PASSWORD') };
const plainUser = { user: env('HM_LIVE_PLAIN_USER'), password: env('HM_LIVE_PLAIN_PASSWORD') };
// A free client identifier: pairing works without metadata, shown as not checked.
const pairingClient = 'browser-pairing';

const test = base.extend<{ problems: string[]; allowStatus: (status: number) => void }>({
  problems: [
    async ({ page }, use) => {
      const problems: string[] = [];
      // Any CSP violation, on any page; console and page errors on the gateway's pages
      // (Home Assistant's login page is not ours to judge).
      page.on('console', (msg) => {
        const text = msg.text();
        if (text.includes('Content Security Policy') || text.startsWith('CSP violation')) problems.push(text);
        else if (msg.type() === 'error' && msg.location().url.startsWith(gateway)) problems.push(text);
      });
      page.on('pageerror', (err) => page.url().startsWith(gateway) && problems.push(err.message));
      // Every page of ours comes with its strict policy (redirects are no pages).
      page.on('response', (res) => {
        const isPage = (res.headers()['content-type'] ?? '').startsWith('text/html') && (res.status() < 300 || res.status() >= 400);
        if (res.url().startsWith(gateway) && isPage && !(res.headers()['content-security-policy'] ?? '').includes("default-src 'none'"))
          problems.push(`no CSP: ${new URL(res.url()).pathname}`);
      });
      await page.addInitScript(() => {
        document.addEventListener('securitypolicyviolation', (e) =>
          console.error(`CSP violation: ${e.violatedDirective} ${e.blockedURI}`),
        );
      });
      await use(problems);
      expect(problems).toEqual([]);
    },
    { auto: true },
  ],
  // A refusal page is answered with its status, which Chromium reports on the console;
  // a test names the statuses it expects.
  allowStatus: async ({ problems }, use) => {
    const allowed: number[] = [];
    await use((status) => allowed.push(status));
    const expected = (p: string) => allowed.some((s) => p.startsWith(`Failed to load resource: the server responded with a status of ${s} `));
    problems.splice(0, problems.length, ...problems.filter((p) => !expected(p)));
  },
});
// Chromium accepts exactly the keys of Home Assistant's and the gateway's certificates of
// the test CA; Node's fetch trusts the test CA through NODE_EXTRA_CA_CERTS.
// Two sign-ins at Home Assistant, a token poll and an MCP call per test: more than the default.
test.setTimeout(120_000);
test.use({ launchOptions: { args: [`--ignore-certificate-errors-spki-list=${env('HM_LIVE_SPKI')}`] } });

/** The gateway's own messages (internal/i18n), so the pages are checked in their language. */
type Lang = 'de' | 'en';
const catalogs: Record<Lang, Record<string, string>> = {
  de: JSON.parse(readFileSync(new URL('../../internal/i18n/messages/de.json', import.meta.url), 'utf8')),
  en: JSON.parse(readFileSync(new URL('../../internal/i18n/messages/en.json', import.meta.url), 'utf8')),
};

function msg(lang: Lang, key: string, args: Record<string, string> = {}): string {
  const text = catalogs[lang][key];
  if (text === undefined) throw new Error(`no message ${key}`);
  return text.replace(/\{([a-z_]+)\}/g, (_, name: string) => args[name] ?? `{${name}}`);
}

function langOf(project: string): Lang {
  if (project !== 'de' && project !== 'en') throw new Error(`unexpected project ${project}`);
  return project;
}

/** The agent's view of the gateway: its metadata, as an MCP client discovers it. */
async function discover(): Promise<{ resource: string; authorize: string; token: string; device: string }> {
  const prm = await (await fetch(`${gateway}/.well-known/oauth-protected-resource`)).json();
  const as = await (await fetch(`${gateway}/.well-known/oauth-authorization-server`)).json();
  expect(prm.authorization_servers).toEqual([gateway]);
  expect(as.code_challenge_methods_supported).toEqual(['S256']);
  expect(as.client_id_metadata_document_supported).toBe(true);
  return {
    resource: prm.resource,
    authorize: as.authorization_endpoint,
    token: as.token_endpoint,
    device: as.device_authorization_endpoint,
  };
}

/** loopback is the agent's redirect listener on 127.0.0.1 with a free port. */
class Loopback {
  private server: Server;
  private received: URL[] = [];
  private waiting: ((u: URL) => void)[] = [];
  redirectURI = '';

  constructor() {
    this.server = createServer((req, res) => {
      const u = new URL(req.url ?? '/', 'http://127.0.0.1');
      if (u.pathname !== '/callback') {
        res.writeHead(404).end();
        return;
      }
      res.writeHead(200, { 'Content-Type': 'text/plain; charset=utf-8' }).end('ok');
      const next = this.waiting.shift();
      if (next) next(u);
      else this.received.push(u);
    });
  }

  async start(): Promise<void> {
    await new Promise<void>((resolve) => this.server.listen(0, '127.0.0.1', resolve));
    const addr = this.server.address();
    if (addr === null || typeof addr === 'string') throw new Error('no port');
    this.redirectURI = `http://127.0.0.1:${addr.port}/callback`;
  }

  /** next is the next redirect that reaches the agent. */
  next(): Promise<URL> {
    const u = this.received.shift();
    if (u) return Promise.resolve(u);
    return new Promise((resolve) => this.waiting.push(resolve));
  }

  /** count is how many redirects arrived and were not taken yet. */
  count(): number {
    return this.received.length;
  }

  close(): Promise<void> {
    return new Promise((resolve) => this.server.close(() => resolve()));
  }
}

function base64url(b: Buffer): string {
  return b.toString('base64url');
}

/** An authorization request as an agent starts it: PKCE S256, state, resource. */
function authorizationRequest(endpoint: string, redirectURI: string, resource: string) {
  const verifier = base64url(randomBytes(48));
  const challenge = base64url(createHash('sha256').update(verifier).digest());
  const state = base64url(randomBytes(16));
  const url = new URL(endpoint);
  url.search = new URLSearchParams({
    response_type: 'code',
    client_id: clientID,
    redirect_uri: redirectURI,
    code_challenge: challenge,
    code_challenge_method: 'S256',
    state,
    resource,
  }).toString();
  return { url: url.toString(), verifier, state };
}

/** signInAtHA signs in on Home Assistant's real login page, as a person types it. */
async function signInAtHA(page: Page, who: { user: string; password: string }): Promise<void> {
  await expect(page).toHaveURL(/\/auth\/authorize\?/);
  const username = page.locator('input[autocomplete="username"]');
  const password = page.locator('input[type="password"]');
  await username.fill(who.user);
  await password.fill(who.password);
  await password.press('Enter');
}

/** expectPageHeading checks the language and the title of a page of ours. */
async function expectPageHeading(page: Page, lang: Lang, title: string): Promise<void> {
  await expect(page.locator('html')).toHaveAttribute('lang', lang);
  await expect(page.getByRole('heading', { level: 1, name: msg(lang, title) })).toBeVisible();
}

/** templateRadio is the card of a template on the consent page, by its accessible name. */
function templateRadio(page: Page, lang: Lang, key: string) {
  const title = msg(lang, key);
  return page.getByRole('radio', { name: new RegExp(`^${title.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`) });
}

/** checkConsentPage checks what every consent page shows. */
async function checkConsentPage(page: Page, lang: Lang): Promise<void> {
  await expect(page).toHaveURL(`${gateway}/oauth/consent`);
  await expectPageHeading(page, lang, 'page_consent_title');
  await expect(page.getByText(msg(lang, 'page_signed_in_as', { user: admin.user }))).toBeVisible();
  // Base templates are offered, the hidden one (e2e/oauth_test.go hides it) is not.
  await expect(templateRadio(page, lang, 'template_read_only_title')).toBeVisible();
  await expect(templateRadio(page, lang, 'template_voice_cautious_title')).toBeVisible();
  await expect(templateRadio(page, lang, 'template_light_climate_title')).toHaveCount(0);
  await expect(page.locator('input[name="template"][value^="hm-light-climate@"]')).toHaveCount(0);
  // The most cautious comes first and is chosen.
  await expect(templateRadio(page, lang, 'template_read_only_title')).toBeChecked();
}

/** admitOnConsent chooses a template card, names the agent and admits it. */
async function admitOnConsent(page: Page, lang: Lang, name: string): Promise<void> {
  // A person clicks the card's label, not the radio button.
  await page.locator('label', { hasText: msg(lang, 'template_voice_cautious_title') }).click();
  await expect(templateRadio(page, lang, 'template_voice_cautious_title')).toBeChecked();
  await page.getByLabel(msg(lang, 'page_consent_name')).fill(name);
  await page.getByRole('button', { name: msg(lang, 'page_consent_approve') }).click();
}

type Tokens = { access_token: string; refresh_token: string; token_type: string };

async function postForm(url: string, form: Record<string, string>): Promise<{ status: number; body: Record<string, unknown> }> {
  const res = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams(form),
  });
  return { status: res.status, body: await res.json() };
}

type Permissions = { devices: { entity_id: string; actions: Record<string, string> }[] };

/** callMCP calls list_my_permissions with the access token, as an agent does. */
async function callMCP(resource: string, accessToken: string): Promise<Permissions> {
  const res = await fetch(resource, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/json, text/event-stream',
      Authorization: `Bearer ${accessToken}`,
    },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'list_my_permissions', arguments: {} } }),
  });
  expect(res.status).toBe(200);
  const answer = await res.json();
  expect(answer.error).toBeUndefined();
  expect(answer.result.isError ?? false).toBe(false);
  return answer.result.structuredContent;
}

/** expectChosenTemplate checks with the agent's token that it got the chosen card: the
 *  cautious voice assistant asks before a lock opens (read only would not list it). */
async function expectChosenTemplate(resource: string, accessToken: string): Promise<void> {
  await expect
    .poll(async () => (await callMCP(resource, accessToken)).devices.find((d) => d.entity_id === 'lock.front_door')?.actions.unlock, {
      timeout: 60_000,
    })
    .toBe('ask');
}

test('an agent is admitted through the authorization code flow with PKCE', async ({ page }, info) => {
  const lang = langOf(info.project.name);
  const name = `Browser PKCE ${lang}`;
  const meta = await discover();
  const agent = new Loopback();
  await agent.start();
  try {
    const req = authorizationRequest(meta.authorize, agent.redirectURI, meta.resource);
    const consent = page.waitForResponse((r) => r.url() === `${gateway}/oauth/consent`);
    await page.goto(req.url);
    await signInAtHA(page, admin);
    await checkConsentPage(page, lang);
    // The form may lead to the agent's loopback origin, and nowhere else.
    const origin = new URL(agent.redirectURI).origin;
    expect((await consent).headers()['content-security-policy']).toContain(`form-action 'self' ${origin};`);
    const host = new URL(clientID).host;
    await expect(page.getByText(msg(lang, 'page_consent_verified', { host }))).toBeVisible();
    await expect(page.getByText(msg(lang, 'page_consent_return', { host: new URL(agent.redirectURI).host }))).toBeVisible();
    const redirected = agent.next();
    await admitOnConsent(page, lang, name);
    await expect.poll(() => page.url().startsWith(`${agent.redirectURI}?`), { message: 'back at the agent' }).toBe(true);
    const back = await redirected;
    expect(back.searchParams.get('state')).toBe(req.state);
    expect(back.searchParams.get('iss')).toBe(gateway);
    expect(back.searchParams.get('error')).toBeNull();
    const code = back.searchParams.get('code') ?? '';
    expect(code !== '', 'a code').toBe(true);

    const exchange = {
      grant_type: 'authorization_code',
      code,
      redirect_uri: agent.redirectURI,
      client_id: clientID,
      resource: meta.resource,
    };
    // Without the verifier the code is refused; it is still unused after that.
    const noVerifier = await postForm(meta.token, exchange);
    expect(noVerifier).toEqual({ status: 400, body: { error: 'invalid_request' } });
    const wrong = await postForm(meta.token, { ...exchange, code_verifier: base64url(randomBytes(48)) });
    expect(wrong).toEqual({ status: 400, body: { error: 'invalid_grant' } });
    // A code is taken by the first exchange that names it, also with a wrong verifier: it
    // is used up now, so the agent starts again.
    const req2 = authorizationRequest(meta.authorize, agent.redirectURI, meta.resource);
    await page.goto(req2.url);
    await signInAtHA(page, admin);
    await checkConsentPage(page, lang);
    const redirected2 = agent.next();
    await admitOnConsent(page, lang, name);
    await expect.poll(() => page.url().startsWith(`${agent.redirectURI}?`), { message: 'back at the agent' }).toBe(true);
    const back2 = await redirected2;
    expect(back2.searchParams.get('state')).toBe(req2.state);
    const tokens = await postForm(meta.token, { ...exchange, code: back2.searchParams.get('code') ?? '', code_verifier: req2.verifier });
    expect(tokens.status).toBe(200);
    const t = tokens.body as Tokens;
    expect(t.token_type).toBe('Bearer');
    expect(/^hma_/.test(t.access_token) && /^hmr_/.test(t.refresh_token), 'an access and a refresh token').toBe(true);
    // The code cannot be redeemed twice.
    const again = await postForm(meta.token, { ...exchange, code: back2.searchParams.get('code') ?? '', code_verifier: req2.verifier });
    expect(again).toEqual({ status: 400, body: { error: 'invalid_grant' } });

    await expectChosenTemplate(meta.resource, t.access_token);
  } finally {
    await agent.close();
  }
});

test('a denial sends access_denied back and admits nobody', async ({ page, allowStatus }, info) => {
  const lang = langOf(info.project.name);
  const meta = await discover();
  const agent = new Loopback();
  await agent.start();
  try {
    const req = authorizationRequest(meta.authorize, agent.redirectURI, meta.resource);
    await page.goto(req.url);
    await signInAtHA(page, admin);
    await checkConsentPage(page, lang);
    await page.getByLabel(msg(lang, 'page_consent_name')).fill(`Browser denied ${lang}`);
    const redirected = agent.next();
    await page.getByRole('button', { name: msg(lang, 'page_consent_deny') }).click();
    await expect.poll(() => page.url().startsWith(`${agent.redirectURI}?`), { message: 'back at the agent' }).toBe(true);
    const back = await redirected;
    expect(back.searchParams.get('error')).toBe('access_denied');
    expect(back.searchParams.get('code')).toBeNull();
    expect(back.searchParams.get('state')).toBe(req.state);
    expect(back.searchParams.get('iss')).toBe(gateway);
    // The consent session is over: going back to the consent page is refused.
    allowStatus(400);
    await page.goto(`${gateway}/oauth/consent`);
    await expectPageHeading(page, lang, 'page_error_title');
    await expect(page.getByText(msg(lang, 'page_session_expired'))).toBeVisible();
  } finally {
    await agent.close();
  }
});

test('someone who is no administrator cannot admit an agent', async ({ page, allowStatus }, info) => {
  const lang = langOf(info.project.name);
  const meta = await discover();
  const agent = new Loopback();
  await agent.start();
  try {
    const req = authorizationRequest(meta.authorize, agent.redirectURI, meta.resource);
    allowStatus(403);
    const refused = page.waitForResponse((r) => r.url().startsWith(`${gateway}/oauth/ha/callback`));
    await page.goto(req.url);
    await signInAtHA(page, plainUser);
    expect((await refused).status()).toBe(403);
    await expectPageHeading(page, lang, 'page_error_title');
    await expect(page.getByText(msg(lang, 'page_not_admin'))).toBeVisible();
    await expect(page.getByRole('button')).toHaveCount(0);
    // The agent hears nothing: no code, no error.
    expect(agent.count()).toBe(0);
  } finally {
    await agent.close();
  }
});

test('an agent is admitted through a pairing code', async ({ page }, info) => {
  const lang = langOf(info.project.name);
  const name = `Browser pairing ${lang}`;
  const meta = await discover();
  const started = await postForm(meta.device, { client_id: pairingClient, resource: meta.resource });
  // 503: earlier tests left pending pairings of this sender (three at most).
  expect(started.status, `device authorization: ${JSON.stringify(started.body.error)}`).toBe(200);
  const pairing = started.body as { device_code: string; user_code: string; verification_uri: string; interval: number };
  expect(pairing.verification_uri).toBe(`${gateway}/pair`);

  await page.goto(pairing.verification_uri);
  await signInAtHA(page, admin);
  await expect(page).toHaveURL(`${gateway}/pair`);
  await expectPageHeading(page, lang, 'page_pair_title');
  // A person types the code as the agent shows it, in lower case and without the dash.
  await page.getByLabel(msg(lang, 'page_pair_code')).fill(pairing.user_code.replace('-', '').toLowerCase());
  await page.getByRole('button', { name: msg(lang, 'page_pair_submit') }).click();
  await checkConsentPage(page, lang);
  await expect(page.getByText(msg(lang, 'page_consent_unverified', { client: pairingClient }))).toBeVisible();
  await admitOnConsent(page, lang, name);
  await expectPageHeading(page, lang, 'page_consent_title');
  await expect(page.getByText(msg(lang, 'page_admitted'))).toBeVisible();

  // The agent's poll gets the tokens.
  const poll = { grant_type: 'urn:ietf:params:oauth:grant-type:device_code', device_code: pairing.device_code, client_id: pairingClient };
  let answer = await postForm(meta.token, poll);
  let interval = pairing.interval;
  for (let i = 0; answer.status !== 200 && i < 6; i++) {
    expect(['authorization_pending', 'slow_down']).toContain(answer.body.error);
    if (answer.body.error === 'slow_down') interval += 5; // RFC 8628 section 3.5
    await new Promise((resolve) => setTimeout(resolve, (interval + 1) * 1000));
    answer = await postForm(meta.token, poll);
  }
  expect(answer.status).toBe(200);
  const t = answer.body as Tokens;
  expect(/^hma_/.test(t.access_token), 'an access token').toBe(true);
  await expectChosenTemplate(meta.resource, t.access_token);
});
