// Authorization for the "Try it" client. Reads components.securitySchemes from
// the AsyncAPI document and lets the person authorize with any scheme fuge
// supports: http bearer, httpApiKey (query/cookie), oauth2 (authorization code
// + PKCE, implicit, password, client credentials) and openIdConnect.
//
// Defaults come from the document (and its "x-fuge-*" extensions, see
// security.go); everything can be overridden in the panel. Loaded before
// app.js, which calls FugeAuth.init({ spec, h, onChange }).
(() => {
'use strict';

/* ───────────── debug log ───────────── */
// Every step is recorded, with secrets masked, in a log shared by all windows of
// this origin — so the OAuth popup, which closes itself, still leaves a trail.
// Read it in Authorize → Debug log, or run fugeDebug() in the console. To also
// print each step to the console, tick the box in that panel or run
// localStorage.setItem('fuge.debug', '1').
const LOG_KEY = 'fuge.debug.log';
const SECRET_KEY = /^(access_token|id_token|refresh_token|code|token|password|client_secret|clientSecret|code_verifier|bearer|apiKey)$/i;
const mask = (v) => { const t = String(v ?? ''); return t ? `‹${t.length} chars, starts ${JSON.stringify(t.slice(0, 4))}›` : '‹empty›'; };
function scrub(x, depth = 0) {
  if (x == null || typeof x === 'number' || typeof x === 'boolean') return x;
  if (typeof x === 'string') return x.length > 300 ? x.slice(0, 300) + '…' : x;
  if (typeof x !== 'object') return String(x);
  if (depth > 3) return '…';
  if (Array.isArray(x)) return x.slice(0, 20).map((v) => scrub(v, depth + 1));
  return Object.fromEntries(Object.entries(x).map(([k, v]) => [k, SECRET_KEY.test(k) ? mask(v) : scrub(v, depth + 1)]));
}
const scrubUrl = (u) => String(u).replace(/([?#&])(access_token|id_token|refresh_token|code|code_verifier|client_secret|password)=([^&#]*)/gi, (_, p, k, v) => `${p}${k}=${mask(v)}`);
const where = () => (window.name === 'fuge-auth' || window.opener ? 'popup' : 'main');
let logWatcher = () => {};
function log(step, data, w) {
  const e = { t: Date.now(), w: w || where(), step, data: data === undefined ? undefined : scrub(data) };
  try {
    const all = JSON.parse(localStorage.getItem(LOG_KEY) || '[]');
    all.push(e);
    localStorage.setItem(LOG_KEY, JSON.stringify(all.slice(-400)));
  } catch { /* ignore */ }
  try { if (localStorage.getItem('fuge.debug') === '1') console.log(`[fuge:${e.w}] ${step}`, e.data === undefined ? '' : e.data); } catch { /* ignore */ }
  logWatcher();
}
const fmtLog = (e) => `${new Date(e.t).toISOString().slice(11, 23)} [${e.w}] ${e.step}${e.data === undefined ? '' : ' ' + JSON.stringify(e.data)}`;
const logText = () => { try { return JSON.parse(localStorage.getItem(LOG_KEY) || '[]').map(fmtLog).join('\n'); } catch { return ''; } };
window.fugeDebug = () => { const t = logText(); console.log(t); return t; };
window.addEventListener('storage', (ev) => { if (ev.key === LOG_KEY) logWatcher(); });

/* ───────────── OAuth popup callback: relay the result and close ───────────── */
// The identity provider redirects the popup back to this page with
// ?code=…&state=fuge.… (or #access_token=… for implicit). Our own state prefix
// identifies it; the opener matches the state it generated.
{
  const q = new URLSearchParams(location.search);
  const f = new URLSearchParams(location.hash.replace(/^#/, ''));
  let pending = null; // set by the opener right before it opens the popup
  try { pending = JSON.parse(localStorage.getItem('fuge.auth.pending') || 'null'); } catch { /* ignore */ }
  const fresh = pending && Date.now() - pending.t < 5 * 60 * 1000;
  const result = (x) => x.has('code') || x.has('access_token') || x.has('error');
  // Ours if it echoes our state — or, for providers that drop state, if a flow is pending.
  const p = [q, f].find((x) => result(x) && ((x.get('state') || '').startsWith('fuge.') || (fresh && !x.get('state'))));
  log('page.load', { href: scrubUrl(location.href), hasOpener: !!window.opener, windowName: window.name, queryKeys: [...q.keys()], hashKeys: [...f.keys()],
    pending: pending && { state: pending.state, ageMs: Date.now() - pending.t }, pendingFresh: !!fresh });
  if (!p && (result(q) || result(f))) {
    log('callback.rejected', { reason: 'looks like an auth response but the state does not start with "fuge." and no flow is pending', state: (q.get('state') || f.get('state') || null) });
  }
  if (p) {
    const msg = Object.fromEntries(p);
    log('callback.detected', { via: (p.get('state') || '').startsWith('fuge.') ? 'state echo' : 'pending marker (provider dropped state)', keys: Object.keys(msg) }, 'popup');
    if (!msg.state && fresh) msg.state = pending.state;
    try { const bc = new BroadcastChannel('fuge-auth'); bc.postMessage(msg); bc.close(); log('callback.relay.broadcast', 'sent', 'popup'); } catch (err) { log('callback.relay.broadcast.error', String(err), 'popup'); }
    try { if (window.opener) { window.opener.postMessage({ fugeAuth: msg }, location.origin); log('callback.relay.opener', 'sent', 'popup'); } else log('callback.relay.opener', 'skipped: window.opener is null (severed by the provider COOP, or not a popup)', 'popup'); } catch (err) { log('callback.relay.opener.error', String(err), 'popup'); }
    window.FugeAuth = { callback: true };
    document.addEventListener('DOMContentLoaded', () => { document.body.textContent = 'Authorization complete. You can close this window.'; });
    log('callback.close', 'closing in 150 ms', 'popup');
    setTimeout(() => window.close(), 150);
    return;
  }
}

const SUPPORTED = ['http', 'httpApiKey', 'oauth2', 'openIdConnect'];
const FLOWS = [['authorizationCode', 'Authorization code'], ['implicit', 'Implicit'], ['password', 'Password'], ['clientCredentials', 'Client credentials']];
const FLOW_LABEL = Object.fromEntries(FLOWS);
const TYPE_LABEL = { http: 'Bearer token', httpApiKey: 'API key', oauth2: 'OAuth 2.0', openIdConnect: 'OpenID Connect' };

const jget = (s, k) => { try { return JSON.parse(s.getItem(k) || 'null'); } catch { return null; } };
const jset = (s, k, v) => { try { s.setItem(k, JSON.stringify(v)); } catch { /* ignore */ } };

let spec, h, onChange = () => {};
let dialog = null;
const disco = {};      // scheme → {loading, error, doc}
const ui = { busy: false, error: '', note: '', cancel: null, debugOpen: false };

/* ───────────── schemes & state ───────────── */

function schemes() {
  const all = spec?.components?.securitySchemes || {};
  return Object.entries(all).filter(([, d]) => SUPPORTED.includes(d.type)).map(([name, def]) => ({ name, def }));
}
const refName = (r) => String(r?.$ref || '').split('/').pop();
function selectedName() {
  const list = schemes();
  const saved = jget(localStorage, 'fuge.auth.selected');
  if (list.some((s) => s.name === saved)) return saved;
  for (const srv of Object.values(spec.servers || {})) {
    for (const r of srv.security || []) if (list.some((s) => s.name === refName(r))) return refName(r);
  }
  return list[0]?.name;
}
const getScheme = (name) => schemes().find((s) => s.name === name);

// Non-secret overrides persist in localStorage; secrets and tokens in sessionStorage.
const cfgKey = (n) => `fuge.auth.cfg.${n}`;
const secKey = (n) => `fuge.auth.secret.${n}`;
const cfgOf = (n) => jget(localStorage, cfgKey(n)) || {};
const secOf = (n) => jget(sessionStorage, secKey(n)) || {};
const saveCfg = (n, c) => jset(localStorage, cfgKey(n), c);
const saveSec = (n, s) => jset(sessionStorage, secKey(n), s);

const ext = (def, k) => def[`x-fuge-${k}`];
const parseParams = (text) => Object.fromEntries(String(text || '').split('\n').map((l) => l.trim()).filter(Boolean).map((l) => { const i = l.indexOf('='); return i < 0 ? [l, ''] : [l.slice(0, i).trim(), l.slice(i + 1).trim()]; }));
const fmtParams = (o) => Object.entries(o || {}).map(([k, v]) => `${k}=${v}`).join('\n');

// Effective settings: person's override, else the document's default.
function effective(name) {
  const { def } = getScheme(name);
  const c = cfgOf(name);
  const oidc = def.type === 'openIdConnect';
  const flows = oidc ? FLOWS.map((f) => f[0]) : FLOWS.map((f) => f[0]).filter((f) => def.flows?.[f]);
  const flow = flows.includes(c.flow) ? c.flow : flows[0];
  const d = disco[name]?.doc;
  const fdef = def.flows?.[flow] || {};
  const over = c.over?.[flow] || {};
  const scopesAvail = oidc ? (d?.scopes_supported || []).map((s) => [s, '']) : Object.entries(fdef.availableScopes || {});
  const defScopes = ext(def, 'default-scopes') || (oidc ? ['openid'] : []);
  return {
    def, cfg: c, flows, flow, oidc, scopesAvail,
    discoveryUrl: c.discoveryUrl ?? def.openIdConnectUrl ?? '',
    authUrl: over.authUrl ?? (oidc ? d?.authorization_endpoint : fdef.authorizationUrl) ?? '',
    tokenUrl: over.tokenUrl ?? (oidc ? d?.token_endpoint : fdef.tokenUrl) ?? '',
    scopes: over.scopes ?? defScopes,
    clientId: c.clientId ?? ext(def, 'client-id') ?? '',
    redirectUri: c.redirectUri ?? ext(def, 'redirect-uri') ?? (location.origin + location.pathname),
    pkce: c.pkce ?? ext(def, 'use-pkce') ?? 'S256',
    credLoc: c.credLoc ?? ext(def, 'credentials-location') ?? 'body',
    tokenName: c.tokenName ?? ext(def, 'token-name') ?? 'access_token',
    authParams: c.authParams ?? fmtParams(ext(def, 'authorize-params')),
    tokenParams: c.tokenParams ?? fmtParams(ext(def, 'token-params')),
  };
}
function patch(name, fields) { saveCfg(name, { ...cfgOf(name), ...fields }); }
function patchFlow(name, flow, fields) {
  const c = cfgOf(name);
  saveCfg(name, { ...c, over: { ...(c.over || {}), [flow]: { ...(c.over?.[flow] || {}), ...fields } } });
}
function patchSecret(name, fields) { log('secret.patch', { scheme: name, fields }); saveSec(name, { ...secOf(name), ...fields }); onChange(); }

/* ───────────── OAuth machinery ───────────── */

const b64url = (buf) => btoa(String.fromCharCode(...new Uint8Array(buf))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
const rand = (n) => b64url(crypto.getRandomValues(new Uint8Array(n)));

async function discover(name) {
  const e = effective(name);
  disco[name] = { loading: true };
  log('discovery.start', { scheme: name, url: e.discoveryUrl });
  render();
  try {
    const res = await fetch(e.discoveryUrl, { headers: { Accept: 'application/json' } });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    disco[name] = { doc: await res.json() };
    log('discovery.ok', { authorization_endpoint: disco[name].doc.authorization_endpoint, token_endpoint: disco[name].doc.token_endpoint });
  } catch (err) {
    disco[name] = { error: `Discovery failed: ${err.message}` };
    log('discovery.error', err.message + ' (a network error here is usually CORS or the CSP connect-src)');
  }
  render();
}

function waitForCallback(state, ms = 180000) {
  try { localStorage.setItem('fuge.auth.pending', JSON.stringify({ state, t: Date.now() })); } catch (err) { log('wait.pending.error', String(err)); }
  log('wait.start', { state, timeoutMs: ms, origin: location.origin });
  return new Promise((resolve, reject) => {
    let bc;
    const finish = (fn, v, why) => {
      log('wait.finish', { outcome: fn === resolve ? 'resolved' : 'rejected', why });
      try { localStorage.removeItem('fuge.auth.pending'); } catch { /* ignore */ }
      ui.cancel = null; clearTimeout(timer); window.removeEventListener('message', onMsg); bc?.close(); fn(v);
    };
    const take = (m, via) => {
      const match = !!m && m.state === state;
      log('wait.message', { via, stateMatch: match, got: m?.state, want: state, keys: m && Object.keys(m) });
      if (match) finish(resolve, m, `message via ${via}`);
    };
    const onMsg = (e) => {
      if (e.origin !== location.origin || !e.data?.fugeAuth) { log('wait.message.ignored', { via: 'postMessage', origin: e.origin, expected: location.origin, hasFugeAuth: !!e.data?.fugeAuth }); return; }
      take(e.data.fugeAuth, 'postMessage');
    };
    window.addEventListener('message', onMsg);
    try { bc = new BroadcastChannel('fuge-auth'); bc.onmessage = (e) => take(e.data, 'broadcast'); log('wait.listening', 'postMessage + BroadcastChannel'); } catch (err) { log('wait.bc.error', String(err)); }
    const timer = setTimeout(() => finish(reject, new Error('Timed out waiting for the authorization window'), 'timeout'), ms);
    ui.cancel = () => finish(reject, new Error('Cancelled'), 'cancelled by user');
  });
}

async function tokenRequest(name, e, form) {
  const sec = secOf(name);
  const headers = { 'Content-Type': 'application/x-www-form-urlencoded', Accept: 'application/json' };
  const body = new URLSearchParams(form);
  if (e.clientId) {
    if (e.credLoc === 'header' && sec.clientSecret) headers.Authorization = 'Basic ' + btoa(`${e.clientId}:${sec.clientSecret}`);
    else { body.set('client_id', e.clientId); if (sec.clientSecret) body.set('client_secret', sec.clientSecret); }
  }
  for (const [k, v] of Object.entries(parseParams(e.tokenParams))) body.set(k, v);
  log('token.request', { url: e.tokenUrl, grant: form.grant_type, formKeys: [...body.keys()], clientAuth: headers.Authorization ? 'Basic header' : 'body' });
  let res;
  try { res = await fetch(e.tokenUrl, { method: 'POST', headers, body }); }
  catch (err) { log('token.fetch.error', `${err.message} — a network error here is usually CORS on the token endpoint or the page CSP connect-src`); throw new Error(`Could not reach the token endpoint (${err.message}). Check CORS on ${e.tokenUrl}.`); }
  let json = {};
  try { json = await res.json(); } catch { /* non-JSON error body */ }
  log('token.response', { status: res.status, ok: res.ok, keys: Object.keys(json), tokenField: e.tokenName, hasToken: !!json[e.tokenName], error: json.error, error_description: json.error_description, expires_in: json.expires_in });
  if (!res.ok || json.error) throw new Error(json.error_description || json.error || `Token request failed (HTTP ${res.status})`);
  const token = json[e.tokenName];
  if (!token) throw new Error(`Token response has no "${e.tokenName}" field`);
  return { token, expiresAt: json.expires_in ? Date.now() + json.expires_in * 1000 : 0 };
}

async function getToken(name) {
  const e = effective(name);
  const sec = secOf(name);
  const scope = e.scopes.join(' ');
  log('getToken.start', { scheme: name, flow: e.flow, authUrl: e.authUrl, tokenUrl: e.tokenUrl, clientId: e.clientId, redirectUri: e.redirectUri, scopes: e.scopes, pkce: e.pkce, credLoc: e.credLoc, tokenName: e.tokenName });
  if (e.flow === 'clientCredentials') return tokenRequest(name, e, { grant_type: 'client_credentials', ...(scope && { scope }) });
  if (e.flow === 'password') return tokenRequest(name, e, { grant_type: 'password', username: sec.username || '', password: sec.password || '', ...(scope && { scope }) });

  // Authorization code (+PKCE) and implicit run through a popup.
  if (!e.authUrl) throw new Error('Authorization URL is empty');
  const state = 'fuge.' + rand(16);
  const implicit = e.flow === 'implicit';
  const q = { response_type: implicit ? 'token' : 'code', client_id: e.clientId, redirect_uri: e.redirectUri, state, ...(scope && { scope }) };
  let verifier = '';
  if (!implicit && e.pkce !== 'none') {
    verifier = rand(48);
    if (e.pkce === 'S256') {
      if (!crypto.subtle) throw new Error('PKCE S256 needs HTTPS or localhost; choose "plain" or "none", or open this page over HTTPS');
      q.code_challenge = b64url(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier)));
      q.code_challenge_method = 'S256';
    } else { q.code_challenge = verifier; q.code_challenge_method = 'plain'; }
  }
  Object.assign(q, parseParams(e.authParams));
  const authorizeUrl = e.authUrl + (e.authUrl.includes('?') ? '&' : '?') + new URLSearchParams(q);
  log('authorize.request', { url: scrubUrl(authorizeUrl), usedPkce: !!verifier });
  const popup = window.open(authorizeUrl, 'fuge-auth', 'width=520,height=700');
  log('popup.opened', { ok: !!popup });
  if (!popup) throw new Error('The popup was blocked — allow popups for this page and retry');
  ui.note = `Waiting for the authorization window… the provider must redirect to ${e.redirectUri}`; render();
  const res = await waitForCallback(state);
  ui.note = '';
  log('callback.received', { keys: Object.keys(res), error: res.error, error_description: res.error_description, hasAccessToken: !!res.access_token, hasCode: !!res.code, hasIdToken: !!res.id_token, expires_in: res.expires_in });
  if (res.error) throw new Error(res.error_description || res.error);
  if (implicit && !res.access_token) throw new Error(`The response has no access_token (it returned: ${Object.keys(res).filter((k) => k !== 'state').join(', ') || 'nothing'}). Check the provider allows the implicit flow with response_type=token.`);
  if (implicit) return { token: res.access_token, expiresAt: res.expires_in ? Date.now() + Number(res.expires_in) * 1000 : 0 };
  return tokenRequest(name, e, { grant_type: 'authorization_code', code: res.code, redirect_uri: e.redirectUri, ...(verifier && { code_verifier: verifier }) });
}

/* ───────────── what the connection uses ───────────── */

function credential(name) {
  const s = getScheme(name);
  if (!s) return null;
  const sec = secOf(name);
  switch (s.def.type) {
    case 'http': return sec.bearer ? { token: sec.bearer } : null;
    case 'httpApiKey': return sec.apiKey ? { apiKey: sec.apiKey } : null;
    default: return sec.token ? { token: sec.token, expiresAt: sec.expiresAt } : null;
  }
}

// prepare(url) → { url, token, warning } for the connect handshake.
function prepare(url) {
  const name = selectedName();
  const s = name && getScheme(name);
  const cred = s && credential(name);
  log('prepare', { scheme: name, type: s?.def.type, hasCredential: !!cred, kind: cred ? (cred.token ? 'token' : 'apiKey') : 'none', expired: !!(cred?.expiresAt && cred.expiresAt < Date.now()) });
  if (!cred) return { url, token: '' };
  if (cred.token) return { url, token: cred.token, warning: cred.expiresAt && cred.expiresAt < Date.now() ? 'The access token has expired' : '' };
  try {
    const u = new URL(url);
    if (s.def.in === 'query') { u.searchParams.set(s.def.name, cred.apiKey); return { url: u.toString(), token: '' }; }
    if (u.host === location.host) {
      document.cookie = `${encodeURIComponent(s.def.name)}=${encodeURIComponent(cred.apiKey)}; path=/; SameSite=Strict`;
      return { url, token: '' };
    }
    return { url, token: '', warning: `Cookie "${s.def.name}" can only be set when the websocket is on this host (${location.host})` };
  } catch { return { url, token: '' }; }
}

function summary() {
  const list = schemes();
  if (!list.length) return null;
  const name = selectedName();
  const cred = credential(name);
  const expired = cred?.expiresAt && cred.expiresAt < Date.now();
  return { name, authorized: !!cred && !expired, expired: !!expired, label: !cred ? 'Authorize' : expired ? 'Token expired' : `Authorized · ${name}` };
}

/* ───────────── panel ───────────── */

function field(label, input, hint) {
  return h('label', { class: 'af' }, h('span', { class: 'af-l' }, label), input, hint && h('span', { class: 'af-h' }, hint));
}
function text(value, oninput, o = {}) {
  return h('input', { class: 'af-i mono', type: o.type || 'text', value: value ?? '', placeholder: o.placeholder || '', autocomplete: 'off', spellcheck: 'false', oninput: (ev) => oninput(ev.target.value) });
}
// A token is shown in full (wrapping, selectable) with a Copy button, so it can
// be taken to other tools. Secrets such as passwords stay masked.
function tokenField(label, value, oninput, placeholder) {
  const area = h('textarea', { class: 'af-i mono token-area', rows: 4, spellcheck: 'false', placeholder, value: value || '', oninput: (ev) => oninput(ev.target.value) });
  const copy = h('button', { class: 'btn sm', type: 'button', onclick: async () => {
    try { await navigator.clipboard.writeText(area.value); } catch { area.select(); document.execCommand('copy'); }
    copy.textContent = 'Copied'; setTimeout(() => (copy.textContent = 'Copy'), 1200);
  } }, 'Copy');
  return h('div', { class: 'af' }, h('div', { class: 'row sb' }, h('span', { class: 'af-l' }, label), copy), area);
}
function select(options, value, onchange) {
  const el = h('select', { class: 'af-i', onchange: (ev) => onchange(ev.target.value) }, options.map(([v, l]) => h('option', { value: v }, l)));
  el.value = value;
  return el;
}

function fieldsFor(name) {
  const { def } = getScheme(name);
  const sec = secOf(name);
  if (def.type === 'http') {
    return [tokenField('Bearer token', sec.bearer, (v) => patchSecret(name, { bearer: v }), def.bearerFormat || 'token'),
      h('span', { class: 'af-h' }, 'Sent in the Centrifuge connect command.')];
  }
  if (def.type === 'httpApiKey') {
    return [field(`${def.name} (${def.in})`, text(sec.apiKey, (v) => patchSecret(name, { apiKey: v }), { type: 'password' }),
      def.in === 'query' ? 'Appended to the websocket URL as a query parameter.' : 'Set as a cookie before connecting (same host only).')];
  }
  return oauthFields(name);
}

function oauthFields(name) {
  const e = effective(name);
  const sec = secOf(name);
  const out = [];
  const rerender = () => render();

  if (e.oidc) {
    const d = disco[name];
    out.push(field('Discovery URL', h('div', { class: 'row' },
      text(e.discoveryUrl, (v) => patch(name, { discoveryUrl: v })),
      h('button', { class: 'btn sm', onclick: () => discover(name) }, d?.loading ? 'Loading…' : d?.doc ? 'Reload' : 'Load')),
    d?.error ? h('span', { class: 'err-text' }, d.error) : d?.doc ? 'Endpoints loaded from the provider.' : 'Load to fill the endpoints below.'));
  }
  out.push(field('Flow', select(e.flows.map((f) => [f, FLOW_LABEL[f]]), e.flow, (v) => { patch(name, { flow: v }); rerender(); })));
  if (e.flow === 'authorizationCode' || e.flow === 'implicit')
    out.push(field('Authorization URL', text(e.authUrl, (v) => patchFlow(name, e.flow, { authUrl: v }))));
  if (e.flow !== 'implicit') out.push(field('Token URL', text(e.tokenUrl, (v) => patchFlow(name, e.flow, { tokenUrl: v }))));
  out.push(field('Client ID', text(e.clientId, (v) => patch(name, { clientId: v }))));
  if (e.flow !== 'implicit') out.push(field('Client secret', text(sec.clientSecret, (v) => patchSecret(name, { clientSecret: v }), { type: 'password' }), e.flow === 'clientCredentials' ? 'Required by most providers.' : 'Leave empty for public clients.'));
  if (e.flow === 'password') {
    out.push(field('Username', text(sec.username, (v) => patchSecret(name, { username: v }))));
    out.push(field('Password', text(sec.password, (v) => patchSecret(name, { password: v }), { type: 'password' })));
  }
  if (e.flow === 'authorizationCode' || e.flow === 'implicit')
    out.push(field('Redirect URI', text(e.redirectUri, (v) => patch(name, { redirectUri: v })), 'Must be registered with the provider; the default is this page.'));
  if (e.flow === 'authorizationCode')
    out.push(field('PKCE', select([['S256', 'S256 (recommended)'], ['plain', 'plain'], ['none', 'none']], e.pkce, (v) => patch(name, { pkce: v }))));

  // scopes: free text plus one-click chips from the document / provider
  const scopeInput = text(e.scopes.join(' '), (v) => patchFlow(name, e.flow, { scopes: v.split(/\s+/).filter(Boolean) }), { placeholder: 'space separated' });
  const chips = e.scopesAvail.map(([s, desc]) => h('button', {
    class: `chip ${e.scopes.includes(s) ? 'on' : ''}`, type: 'button', title: desc || s,
    onclick: () => { const cur = effective(name).scopes; patchFlow(name, e.flow, { scopes: cur.includes(s) ? cur.filter((x) => x !== s) : [...cur, s] }); rerender(); },
  }, s));
  out.push(field('Scopes', h('div', {}, scopeInput, chips.length > 0 && h('div', { class: 'chips' }, chips))));

  out.push(h('details', {}, h('summary', {}, 'Advanced'),
    field('Token field', text(e.tokenName, (v) => patch(name, { tokenName: v || undefined }), { placeholder: 'access_token' }), 'Name of the access token in the token response.'),
    e.flow !== 'implicit' && field('Send client credentials in', select([['body', 'Request body'], ['header', 'Authorization header (Basic)']], e.credLoc, (v) => patch(name, { credLoc: v }))),
    (e.flow === 'authorizationCode' || e.flow === 'implicit') && field('Extra authorize parameters', h('textarea', { class: 'af-i mono', rows: 2, placeholder: 'audience=api', value: e.authParams, oninput: (ev) => patch(name, { authParams: ev.target.value }) }), 'One key=value per line.'),
    e.flow !== 'implicit' && field('Extra token parameters', h('textarea', { class: 'af-i mono', rows: 2, placeholder: 'resource=api', value: e.tokenParams, oninput: (ev) => patch(name, { tokenParams: ev.target.value }) }), 'One key=value per line.')));

  const expiry = sec.expiresAt ? (sec.expiresAt < Date.now() ? 'expired' : `expires in ${Math.max(1, Math.round((sec.expiresAt - Date.now()) / 60000))} min`) : '';
  out.push(h('div', { class: 'af-actions' },
    h('button', { class: 'btn primary', disabled: ui.busy, onclick: async () => {
      log('ui.getToken.click', { scheme: name });
      ui.busy = true; ui.error = ''; ui.note = ''; render();
      try { const t = await getToken(name); log('ui.token.obtained', { token: t.token, expiresAt: t.expiresAt }); patchSecret(name, { token: t.token, expiresAt: t.expiresAt }); }
      catch (err) { ui.error = err.message; log('ui.getToken.error', err.message); }
      ui.busy = false; ui.note = ''; render();
      log('ui.rendered', { dialogOpen: !!dialog?.open, tokenFieldLength: dialog?.querySelector('.token-area')?.value.length ?? null, shownError: ui.error || null });
    } }, ui.busy ? 'Waiting…' : 'Get token'),
    ui.busy && ui.cancel && h('button', { class: 'btn', onclick: () => ui.cancel?.() }, 'Cancel'),
    sec.token && h('button', { class: 'btn danger', onclick: () => { patchSecret(name, { token: '', expiresAt: 0 }); render(); } }, 'Clear token')));
  out.push(tokenField(`Access token${expiry ? ` (${expiry})` : ''}`, sec.token, (v) => patchSecret(name, { token: v, expiresAt: 0 }), 'obtained with “Get token”, or paste one'));
  return out;
}

function debugPanel() {
  const area = h('textarea', { class: 'af-i mono log-area', rows: 12, readonly: true, spellcheck: 'false', wrap: 'off', value: logText() });
  logWatcher = () => { if (area.isConnected) { area.value = logText(); area.scrollTop = area.scrollHeight; } };
  const copy = h('button', { class: 'btn sm', type: 'button', onclick: async () => {
    try { await navigator.clipboard.writeText(logText()); } catch { area.select(); document.execCommand('copy'); }
    copy.textContent = 'Copied'; setTimeout(() => (copy.textContent = 'Copy'), 1200);
  } }, 'Copy');
  const consoleOn = (() => { try { return localStorage.getItem('fuge.debug') === '1'; } catch { return false; } })();
  return h('details', { open: ui.debugOpen, ontoggle: (ev) => { ui.debugOpen = ev.target.open; } },
    h('summary', {}, 'Debug log'),
    h('div', { class: 'af-h' }, 'Every step of the authorization is recorded here, including what the popup saw. Secrets are masked. Share this when reporting a problem.'),
    area,
    h('div', { class: 'row' }, copy,
      h('button', { class: 'btn sm', type: 'button', onclick: () => { try { localStorage.removeItem(LOG_KEY); } catch { /* ignore */ } area.value = ''; } }, 'Clear'),
      h('label', { class: 'row' }, h('input', { type: 'checkbox', checked: consoleOn, onchange: (ev) => { try { localStorage.setItem('fuge.debug', ev.target.checked ? '1' : '0'); } catch { /* ignore */ } } }), 'Also print to the browser console')));
}

function render() {
  if (!dialog?.open) { log('render.skipped', 'dialog is not open'); return; }
  const list = schemes();
  const name = selectedName();
  const body = dialog.querySelector('.dlg-body');
  const keep = dialog.querySelector('.dlg-body')?.scrollTop || 0;
  body.replaceChildren(
    list.length > 1 && field('Scheme', select(list.map((s) => [s.name, `${s.name} — ${TYPE_LABEL[s.def.type]}`]), name, (v) => { jset(localStorage, 'fuge.auth.selected', v); ui.error = ''; render(); onChange(); })),
    getScheme(name).def.description && h('p', { class: 'muted' }, getScheme(name).def.description),
    ...fieldsFor(name),
    ui.note && h('div', { class: 'muted' }, ui.note),
    ui.error && h('div', { class: 'err-text' }, ui.error),
    debugPanel());
  body.scrollTop = keep;
  if (selectedIsOidcUnloaded(name)) discover(name);
}
function selectedIsOidcUnloaded(name) {
  const d = getScheme(name)?.def;
  return d?.type === 'openIdConnect' && !disco[name] && effective(name).discoveryUrl;
}

function open() {
  if (!dialog) {
    dialog = h('dialog', { class: 'dlg' },
      h('div', { class: 'dlg-head' }, h('b', {}, 'Authorize'), h('button', { class: 'btn sm', onclick: () => dialog.close() }, 'Close')),
      h('div', { class: 'dlg-body' }));
    dialog.addEventListener('click', (ev) => { if (ev.target === dialog) dialog.close(); });
    dialog.addEventListener('close', onChange);
    document.body.append(dialog);
  }
  dialog.showModal();
  render();
}

window.FugeAuth = {
  callback: false,
  init(o) { spec = o.spec; h = o.h; onChange = o.onChange || onChange; log('init', { schemes: schemes().map((x) => `${x.name} (${x.def.type})`), selected: selectedName(), redirectDefault: location.origin + location.pathname }); },
  log,
  schemes, summary, open, prepare,
  // The operation's security requirements, as scheme names (OR).
  operationSchemes: (refs) => (refs || []).map(refName).filter((n) => getScheme(n)),
};
})();
