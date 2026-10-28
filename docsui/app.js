// AsyncAPI reference + "try it" client for the fuge-generated Centrifuge API.
// Dependency-free: renders the AsyncAPI document and talks the Centrifuge JSON client
// protocol directly over a browser WebSocket.
(() => {
'use strict';
if (window.FugeAuth?.callback) return; // OAuth popup relaying its result (see auth.js)

// Server-provided settings (see DocsOptions in docs.go), injected as <meta name="fuge-*"> tags.
const meta = (n, d) => { const el = document.querySelector(`meta[name="fuge-${n}"]`); return el ? el.content : d; };
const CFG = { specJson: meta('spec-json', 'asyncapi.json'), specYaml: meta('spec-yaml', 'asyncapi.yaml'), wsUrl: meta('ws-url', '') };

/* ───────────────────────── helpers ───────────────────────── */

const h = (tag, attrs, ...kids) => {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'value') el.value = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false) continue;
    el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return el;
};
const store = {
  get: (s, k) => { try { return s.getItem(k); } catch { return null; } },
  set: (s, k, v) => { try { s.setItem(k, v); } catch { /* ignore */ } },
};
const pretty = (v) => JSON.stringify(v, null, 2);
const clock = () => new Date().toLocaleTimeString([], { hour12: false }) + '.' + String(Date.now() % 1000).padStart(3, '0');
const copyText = async (text, btn) => {
  try { await navigator.clipboard.writeText(text); const o = btn.textContent; btn.textContent = 'Copied'; setTimeout(() => (btn.textContent = o), 1200); } catch { /* ignore */ }
};

/* ───────────────────────── spec model ───────────────────────── */

let spec = null;

function pointer(ref) {
  if (!ref.startsWith('#/')) return undefined;
  let cur = spec;
  for (const part of ref.slice(2).split('/')) {
    cur = cur?.[decodeURIComponent(part).replace(/~1/g, '/').replace(/~0/g, '~')];
  }
  return cur;
}
function deref(node) {
  for (let i = 0; node && node.$ref && i < 20; i++) node = pointer(node.$ref);
  return node;
}
const refName = (ref) => ref.split('/').pop();

function buildModel() {
  const ops = Object.entries(spec.operations || {}).map(([id, raw]) => {
    const op = deref(raw);
    const channel = deref(op.channel) || {};
    const kind = op['x-fuge-operation-kind'] || (op.reply ? 'rpc' : op.action === 'send' ? 'broadcast' : 'receive');
    const messages = (op.messages || []).map(deref);
    const replies = (op.reply?.messages || []).map(deref);
    return {
      id, op, channel, kind, address: channel.address || id,
      title: op.title || id, summary: op.summary || channel.summary || '',
      description: op.description || channel.description || '',
      request: messages[0], replies, tags: (op.tags || []).map((t) => t.name),
      permissions: op['x-permissions'], security: op.security,
    };
  });
  return { ops, rpc: ops.filter((o) => o.kind === 'rpc'), channels: ops.filter((o) => o.kind !== 'rpc') };
}

/* ───────────────────────── example generation ───────────────────────── */

function mergeAllOf(s) {
  if (!s?.allOf) return s;
  const out = { ...s };
  delete out.allOf;
  for (const part of s.allOf.map((p) => mergeAllOf(deref(p)))) {
    out.properties = { ...(out.properties || {}), ...(part.properties || {}) };
    out.required = [...(out.required || []), ...(part.required || [])];
    out.type = out.type || part.type;
  }
  return out;
}
function example(schema, depth = 0) {
  schema = mergeAllOf(deref(schema));
  if (!schema || depth > 6) return null;
  if (schema.const !== undefined) return schema.const;
  if (schema.examples?.length) return schema.examples[0];
  if (schema.example !== undefined) return schema.example;
  if (schema.default !== undefined) return schema.default;
  if (schema.enum?.length) return schema.enum[0];
  const alt = schema.oneOf?.[0] || schema.anyOf?.[0];
  if (alt) return example(alt, depth + 1);
  const type = Array.isArray(schema.type) ? schema.type.find((t) => t !== 'null') : schema.type;
  switch (type) {
    case 'object': case undefined: {
      if (!schema.properties) return type === 'object' ? {} : null;
      const out = {};
      for (const [k, v] of Object.entries(schema.properties)) out[k] = example(v, depth + 1);
      return out;
    }
    case 'array': return [example(schema.items || {}, depth + 1)];
    case 'integer': return schema.minimum ?? 0;
    case 'number': return schema.minimum ?? 0;
    case 'boolean': return false;
    case 'string':
      switch (schema.format) {
        case 'date-time': return new Date().toISOString();
        case 'date': return new Date().toISOString().slice(0, 10);
        case 'email': return 'user@example.com';
        case 'uuid': return '00000000-0000-4000-8000-000000000000';
        case 'uri': return 'https://example.com';
        default: return 'string';
      }
    default: return null;
  }
}

/* ───────────────────────── schema renderer ───────────────────────── */

function typeLabel(s) {
  if (!s) return 'any';
  if (s.$ref) return refName(s.$ref);
  const d = deref(s);
  if (d.enum && !d.type) return 'enum';
  let t = Array.isArray(d.type) ? d.type.join(' | ') : d.type || (d.properties ? 'object' : d.oneOf ? 'oneOf' : d.anyOf ? 'anyOf' : 'any');
  if (t === 'array') t = `${typeLabel(d.items)}[]`;
  if (d.format) t += ` <${d.format}>`;
  return t;
}
function constraints(s) {
  const c = [];
  if (s.enum) c.push(`enum: ${s.enum.map((e) => JSON.stringify(e)).join(', ')}`);
  for (const k of ['minLength', 'maxLength', 'minimum', 'maximum', 'exclusiveMinimum', 'exclusiveMaximum', 'minItems', 'maxItems', 'multipleOf'])
    if (s[k] !== undefined) c.push(`${k}: ${s[k]}`);
  if (s.pattern) c.push(`pattern: ${s.pattern}`);
  if (s.default !== undefined) c.push(`default: ${JSON.stringify(s.default)}`);
  if (s.const !== undefined) c.push(`const: ${JSON.stringify(s.const)}`);
  if (s.examples?.length) c.push(`example: ${JSON.stringify(s.examples[0])}`);
  if (s.nullable) c.push('nullable');
  if (s.readOnly) c.push('readOnly');
  return c;
}
function childrenOf(s) {
  s = mergeAllOf(s);
  if (s.properties) return { kind: 'props', props: s.properties, required: s.required || [] };
  if (s.type === 'array' && s.items) {
    const it = deref(s.items);
    if (it?.properties || it?.allOf) return { kind: 'item', schema: s.items };
  }
  const alts = s.oneOf || s.anyOf;
  if (alts) return { kind: 'alts', alts, label: s.oneOf ? 'One of' : 'Any of' };
  return null;
}
function renderProps(s, seen) {
  s = mergeAllOf(deref(s));
  if (!s) return h('div', { class: 'muted' }, 'No schema');
  const kids = childrenOf(s);
  if (kids?.kind === 'props') {
    const entries = Object.entries(kids.props);
    if (!entries.length) return h('div', { class: 'schema' }, h('div', { class: 'prop muted' }, 'Empty object'));
    return h('div', { class: 'schema' }, entries.map(([name, ps]) => renderProp(name, ps, kids.required.includes(name), seen)));
  }
  return h('div', { class: 'schema' }, renderProp(null, s, false, seen));
}
function renderProp(name, raw, required, seen) {
  const ref = raw.$ref;
  const s = mergeAllOf(deref(raw)) || {};
  const head = h('div', { class: 'prop-head' },
    name != null && h('span', { class: 'pname' }, name),
    h('span', { class: 'ptype' }, typeLabel(raw)),
    required && h('span', { class: 'req' }, 'required'));
  const cons = constraints(s);
  const el = h('div', { class: 'prop' }, head,
    s.description && h('div', { class: 'pdesc' }, s.description),
    cons.length > 0 && h('div', { class: 'cons' }, cons.map((c) => h('span', { class: 'pill' }, c))));
  const kids = childrenOf(s);
  if (kids && !(ref && seen.has(ref))) {
    const next = new Set(seen); if (ref) next.add(ref);
    let inner;
    if (kids.kind === 'props') inner = renderProps(s, next);
    else if (kids.kind === 'item') inner = renderProps(kids.schema, next);
    else inner = h('div', {}, kids.alts.map((a, i) => h('div', {}, h('div', { class: 'muted' }, `${kids.label} #${i + 1}`), renderProps(a, next))));
    const d = h('details', { open: seen.size < 1 }, h('summary', {}, kids.kind === 'item' ? 'Item properties' : 'Properties'), h('div', { class: 'nested' }, inner));
    el.append(d);
  } else if (kids && ref) {
    el.append(h('div', { class: 'muted' }, '↻ recursive reference'));
  }
  return el;
}

/* ───────────────────────── Centrifuge client ───────────────────────── */

class ProtocolError extends Error {
  constructor(code, message, temporary) { super(message); this.name = 'ProtocolError'; this.code = code; this.temporary = !!temporary; }
}

class Client {
  constructor() {
    this.ws = null; this.nextId = 1; this.pending = new Map(); this.subs = new Map();
    this.status = 'disconnected'; this.statusText = 'Disconnected';
    this.onStatus = () => {}; this.onPush = () => {}; this.closing = false;
  }
  _set(status, text) { this.status = status; this.statusText = text; this.onStatus(); }

  connect(url, token) {
    return new Promise((resolve, reject) => {
      this.closing = false; this._set('connecting', 'Connecting…');
      let ws;
      try { ws = new WebSocket(url); } catch (e) { this._set('error', 'Invalid URL'); reject(e); return; }
      this.ws = ws;
      let settled = false;
      ws.onopen = () => {
        const id = this._send({ connect: { token: token || '', name: 'asyncdocs' } });
        this.pending.set(id, {
          resolve: (r) => { settled = true; this._set('connected', 'Connected'); resolve(r); },
          reject: (e) => { settled = true; this._set('error', e.message || 'Rejected'); reject(e); },
        });
      };
      ws.onmessage = (ev) => this._frame(ev.data);
      ws.onerror = () => { if (!settled) { settled = true; this._set('error', 'Connection failed'); reject(new Error('WebSocket connection failed')); } };
      ws.onclose = (ev) => {
        for (const p of this.pending.values()) p.reject(new Error('connection closed'));
        this.pending.clear();
        const ids = [...this.subs.keys()];
        this.subs.clear();
        if (this.ws === ws) this.ws = null;
        if (!settled) { settled = true; reject(new Error(`closed (${ev.code})`)); }
        if (!this.closing && this.status === 'connected') this._set('error', `Lost (${ev.code}${ev.reason ? ': ' + ev.reason : ''})`);
        else if (this.status !== 'error') this._set('disconnected', 'Disconnected');
        ids.forEach((c) => this.onPush(c, { type: 'unsubscribe', channel: c }));
      };
    });
  }
  disconnect() { this.closing = true; this.ws?.close(); }
  get connected() { return this.status === 'connected'; }

  _send(cmd) { const id = this.nextId++; this.ws.send(JSON.stringify({ id, ...cmd })); return id; }
  _call(cmd, pick) {
    return new Promise((resolve, reject) => {
      if (!this.connected) { reject(new Error('Not connected')); return; }
      const id = this._send(cmd);
      this.pending.set(id, { resolve: (r) => resolve(pick ? pick(r) : r), reject });
    });
  }
  rpc(method, data) { return this._call({ rpc: { method, data } }, (r) => r.rpc?.data); }
  subscribe(channel) {
    this.subs.set(channel, true);
    return this._call({ subscribe: { channel } }).catch((e) => { this.subs.delete(channel); throw e; });
  }
  unsubscribe(channel) { this.subs.delete(channel); return this._call({ unsubscribe: { channel } }); }

  _frame(raw) {
    for (const line of String(raw).split('\n')) {
      if (!line.trim()) continue;
      let r; try { r = JSON.parse(line); } catch { continue; }
      this._reply(r);
    }
  }
  _reply(r) {
    if (Object.keys(r).length === 0) { this.ws?.send('{}'); return; } // ping → pong
    if (!r.id) {
      const p = r.push;
      if (!p) return;
      if (p.pub) this.onPush(p.channel, { type: 'pub', data: p.pub.data });
      else if (p.unsubscribe) { this.subs.delete(p.channel); this.onPush(p.channel, { type: 'unsubscribe', channel: p.channel, info: p.unsubscribe }); }
      return;
    }
    const w = this.pending.get(r.id);
    if (!w) return;
    this.pending.delete(r.id);
    if (r.error) w.reject(new ProtocolError(r.error.code, r.error.message, r.error.temporary));
    else w.resolve(r);
  }
}

/* ───────────────────────── app state ───────────────────────── */

const client = new Client();
const state = {
  model: null, route: '', search: '',
  rpcHistory: new Map(),   // opId → [{...}]
  drafts: new Map(),       // opId → payload text
  feeds: new Map(),        // channel → [{time, data}]
  paused: new Set(),
  snippetTab: 'centrifuge-js',
};
const FEED_MAX = 300;
let refreshLive = () => {};

client.onStatus = () => { renderConn(); refreshLive(); };
client.onPush = (channel, ev) => {
  const feed = state.feeds.get(channel) || [];
  if (ev.type === 'pub') {
    if (state.paused.has(channel)) return;
    feed.unshift({ time: clock(), kind: 'ok', data: ev.data });
  } else {
    feed.unshift({ time: clock(), kind: 'info', data: ev.info ? `Server unsubscribed (${JSON.stringify(ev.info)})` : 'Unsubscribed' });
  }
  feed.length = Math.min(feed.length, FEED_MAX);
  state.feeds.set(channel, feed);
  refreshLive(channel);
  if (ev.type !== 'pub') renderNav();
};

/* ───────────────────────── connection bar ───────────────────────── */

function defaultWsUrl() {
  const saved = store.get(localStorage, 'fuge.url');
  if (saved) return saved;
  if (CFG.wsUrl) return CFG.wsUrl;
  const srv = Object.values(spec.servers || {})[0];
  const path = srv?.pathname || '/connection/websocket';
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${location.host}${path}`;
}
let connUrl = '', connWarning = '';
function renderConn() {
  const root = document.getElementById('conn');
  if (!root) return;
  const busy = client.status === 'connecting';
  root.replaceChildren(
    h('span', { class: `dot ${client.status}` }),
    h('span', { class: 'status', title: client.statusText }, client.statusText),
    h('input', { class: 'url mono', value: connUrl, placeholder: 'wss://host/connection/websocket', list: 'servers', disabled: client.connected || busy, 'aria-label': 'WebSocket URL',
      oninput: (e) => { connUrl = e.target.value; store.set(localStorage, 'fuge.url', connUrl); } }),
    h('datalist', { id: 'servers' }, Object.values(spec.servers || {}).map((s) => h('option', { value: `${s.protocol || 'wss'}://${s.host}${s.pathname || ''}` }))),
    connWarning && h('span', { class: 'conn-warn', title: connWarning }, '⚠'),
    authButton(),
    client.connected
      ? h('button', { class: 'btn', onclick: () => client.disconnect() }, 'Disconnect')
      : h('button', { class: 'btn primary', disabled: busy, onclick: doConnect }, busy ? 'Connecting…' : 'Connect'),
  );
}
function authButton() {
  const a = window.FugeAuth?.summary();
  if (!a) return null;
  return h('button', { class: `btn auth-btn ${a.authorized ? 'ok' : a.expired ? 'warn' : ''}`, title: 'Choose a security scheme and authorize', onclick: () => window.FugeAuth.open() },
    a.authorized ? '🔒 ' : '🔓 ', a.label);
}
async function doConnect() {
  const p = window.FugeAuth ? window.FugeAuth.prepare(connUrl.trim()) : { url: connUrl.trim(), token: '' };
  connWarning = p.warning || '';
  window.FugeAuth?.log('connect.start', { url: p.url.replace(/([?&][^=&]+)=[^&]*/g, '$1=…'), sendsToken: !!p.token, warning: p.warning });
  try { await client.connect(p.url, p.token); window.FugeAuth?.log('connect.ok', { status: client.status }); }
  catch (err) { window.FugeAuth?.log('connect.error', { message: err?.message || String(err), status: client.status, text: client.statusText }); /* status shown in bar */ }
}
async function ensureConnected() {
  if (client.connected) return;
  await doConnect();
  if (!client.connected) throw new Error('Not connected — check the URL and authorization in the top bar');
}

/* ───────────────────────── navigation ───────────────────────── */

function renderNav() {
  const nav = document.getElementById('nav');
  const q = state.search.trim().toLowerCase();
  const match = (...xs) => !q || xs.some((x) => String(x || '').toLowerCase().includes(q));
  const item = (route, label, extra, badge) => h('a', { class: `nav-item ${state.route === route ? 'active' : ''}`, href: `#${route}` },
    h('span', { class: 'label' }, label), badge, extra);
  const m = state.model;
  const schemas = Object.keys(spec.components?.schemas || {});
  nav.replaceChildren(
    ...(match('overview', 'introduction') ? [item('/', 'Overview')] : []),
    ...tagGroups(m.ops.filter((o) => match(o.address, o.title, o.summary, o.tags.join(' '))), item),
    ...group('Schemas', schemas.filter((n) => match(n)).map((n) => item(`/schema/${n}`, n))),
  );
}

// Groups operations by their first tag (like Scalar); untagged operations fall
// back to "RPC methods" / "Channels" by kind.
function tagGroups(ops, item) {
  const groups = new Map();
  for (const o of ops) {
    const g = o.tags[0] || (o.kind === 'rpc' ? 'RPC methods' : 'Channels');
    if (!groups.has(g)) groups.set(g, []);
    groups.get(g).push(o);
  }
  return [...groups].flatMap(([title, list]) => group(title, list.map((o) => item(`/op/${o.id}`, o.address,
    o.kind !== 'rpc' && client.subs.has(o.address) ? h('span', { class: 'live', title: 'Subscribed' }) : null,
    o.kind === 'rpc' ? h('span', { class: 'badge rpc' }, 'RPC') : null))));
}
// Collapsed sidebar groups persist across reloads; a search expands everything
// so matches are never hidden.
const collapsed = new Set((() => { try { return JSON.parse(store.get(localStorage, 'fuge.collapsed') || '[]'); } catch { return []; } })());
function group(title, items) {
  if (!items.length) return [];
  const searching = state.search.trim() !== '';
  const closed = collapsed.has(title) && !searching;
  const toggle = () => {
    collapsed.has(title) ? collapsed.delete(title) : collapsed.add(title);
    store.set(localStorage, 'fuge.collapsed', JSON.stringify([...collapsed]));
    renderNav();
  };
  return [
    h('button', { class: `nav-group ${closed ? 'closed' : ''}`, 'aria-expanded': String(!closed), onclick: toggle },
      h('span', { class: 'chev', 'aria-hidden': 'true' }, '▾'), h('span', { class: 'gtitle' }, title), h('span', { class: 'gcount' }, items.length)),
    ...(closed ? [] : items),
  ];
}

const setNav = (open) => document.querySelector('.shell')?.classList.toggle('nav-open', open);

function route() {
  setNav(false);
  state.route = location.hash.replace(/^#/, '') || '/';
  renderNav();
  refreshLive = () => {};
  const main = document.getElementById('main');
  const [, kind, ...rest] = state.route.split('/');
  const id = decodeURIComponent(rest.join('/'));
  let page;
  if (kind === 'op') {
    const o = state.model.ops.find((x) => x.id === id);
    page = o ? (o.kind === 'rpc' ? rpcPage(o) : channelPage(o)) : notFound();
  } else if (kind === 'schema') {
    page = spec.components?.schemas?.[id] ? schemaPage(id) : notFound();
  } else page = overviewPage();
  main.replaceChildren(h('div', { class: 'page' }, page));
  main.scrollTop = 0;
}
const notFound = () => h('div', {}, h('h1', {}, 'Not found'), h('a', { href: '#/' }, 'Back to overview'));

/* ───────────────────────── pages ───────────────────────── */

function overviewPage() {
  const info = spec.info || {};
  const m = state.model;
  const card = (o) => h('a', { class: 'op-card', href: `#/op/${o.id}` },
    h('div', { class: 'row' }, kindBadge(o), o.permissions && h('span', { class: 'badge neutral' }, `perm ${[].concat(o.permissions).join(', ')}`)),
    h('div', { class: 't mono' }, o.address), h('div', { class: 's' }, o.summary));
  const servers = Object.entries(spec.servers || {});
  const c = info.contact;
  const mailto = c?.email && `mailto:${c.email}?subject=${encodeURIComponent(`${info.title || 'API'} support`)}`;
  return h('div', {},
    h('div', { class: 'row mb8' }, info.version && h('span', { class: 'badge neutral' }, `v${info.version}`), h('span', { class: 'badge neutral' }, `AsyncAPI ${spec.asyncapi}`)),
    h('h1', {}, info.title || 'AsyncAPI'),
    h('div', { class: 'row' }, h('a', { href: CFG.specJson, target: '_blank', rel: 'noopener' }, 'asyncapi.json'), CFG.specYaml && h('a', { href: CFG.specYaml, target: '_blank', rel: 'noopener' }, 'asyncapi.yaml')),
    info.description && h('p', { class: 'lead mt12' }, info.description),
    c && (c.email || c.name || c.url) && h('div', {}, h('h2', {}, 'Developer support'),
      h('div', { class: 'card' }, h('div', { class: 'content row' },
        mailIcon(),
        h('div', {}, c.name && h('div', {}, h('b', {}, c.name)),
          mailto && h('a', { href: mailto }, c.email),
          c.url && h('div', {}, h('a', { href: c.url, target: '_blank', rel: 'noopener noreferrer' }, c.url)))))),
    servers.length > 0 && h('div', {}, h('h2', {}, 'Servers'),
      h('div', { class: 'card' }, h('div', { class: 'content' }, h('dl', { class: 'kv' }, servers.flatMap(([n, s]) => [
        h('dt', {}, n), h('dd', {}, h('code', {}, `${s.protocol}://${s.host}${s.pathname || ''}`),
          s.security?.length ? h('span', { class: 'badge neutral ml8' }, 'auth required') : null)]))))),
    h('h2', {}, 'How it works'),
    h('p', { class: 'lead' }, 'This API runs over a Centrifuge WebSocket. ',
      h('b', {}, 'RPC methods'), ' are request/reply calls; ', h('b', {}, 'channels'), ' are subscriptions the server publishes to. Connect with the bar above, then use the “Try it” panel on any operation.'),
    m.rpc.length > 0 && [h('h2', {}, `RPC methods (${m.rpc.length})`), h('div', { class: 'grid-cards' }, m.rpc.map(card))],
    m.channels.length > 0 && [h('h2', {}, `Channels (${m.channels.length})`), h('div', { class: 'grid-cards' }, m.channels.map(card))],
  );
}
function mailIcon() {
  const span = h('span', { class: 'icon', 'aria-hidden': 'true' });
  span.innerHTML = '<svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="5" width="18" height="14" rx="2"/><path d="m3 7 9 6 9-6"/></svg>'; // static markup
  return span;
}
const kindBadge = (o) => o.kind === 'rpc' ? h('span', { class: 'badge rpc' }, 'RPC') : h('span', { class: 'badge sub' }, 'SUBSCRIBE');

function messageSection(title, msg) {
  if (!msg) return null;
  const payload = msg.payload;
  return h('div', {}, h('h3', {}, title, msg.summary && h('span', { class: 'muted' }, ` — ${msg.summary}`)),
    msg.description && h('p', { class: 'lead' }, msg.description),
    payload ? renderProps(payload, new Set(payload.$ref ? [payload.$ref] : [])) : h('div', { class: 'muted' }, 'No payload'));
}
// Which security schemes satisfy the operation (any one — OR); falls back to the
// servers' requirement, since fuge authenticates the connection.
function securityRow(o) {
  const A = window.FugeAuth;
  let names = A?.operationSchemes(o.security) || [];
  if (!names.length) names = A?.operationSchemes(Object.values(spec.servers || {}).flatMap((s) => s.security || [])) || [];
  if (!names.length) return null;
  return h('div', { class: 'row my10' }, h('span', { class: 'muted' }, 'Security:'),
    names.map((n, i) => [i > 0 && h('span', { class: 'muted' }, 'or'), h('button', { class: 'badge neutral linkish', onclick: () => A.open() }, `🔒 ${n}`)]));
}
function opHeader(o) {
  return h('div', {},
    h('div', { class: 'row' }, kindBadge(o), h('span', { class: 'pill' }, o.address), ...o.tags.map((t) => h('span', { class: 'badge neutral' }, t))),
    h('h1', { class: 'mt10' }, o.title),
    o.summary && o.summary !== o.title && h('p', { class: 'lead' }, o.summary),
    o.description && h('p', {}, o.description),
    o.permissions && h('div', { class: 'callout' }, h('b', {}, 'Requires permission: '), [].concat(o.permissions).join(', ')),
    securityRow(o));
}

function rpcPage(o) {
  const errMsg = o.replies.find((r) => /error/i.test(r?.name || r?.title || ''));
  const okMsgs = o.replies.filter((r) => r !== errMsg);
  return h('div', { class: 'split' },
    h('div', {}, opHeader(o), messageSection('Request', o.request), ...okMsgs.map((r) => messageSection('Reply', r)), messageSection('Error reply', errMsg)),
    h('div', { class: 'sticky' }, rpcTryIt(o), snippetsCard(o)));
}

function channelPage(o) {
  return h('div', { class: 'split' },
    h('div', {}, opHeader(o), messageSection('Published message', o.request)),
    h('div', { class: 'sticky' }, channelTryIt(o), snippetsCard(o)));
}

function schemaPage(name) {
  const s = spec.components.schemas[name];
  return h('div', {}, h('div', { class: 'row' }, h('span', { class: 'badge neutral' }, 'schema')), h('h1', { class: 'mt10' }, name),
    s.description && h('p', { class: 'lead' }, s.description),
    renderProps(s, new Set([`#/components/schemas/${name}`])),
    h('h3', {}, 'Example'), codeBlock(pretty(example(s))));
}

/* ───────────────────────── widgets ───────────────────────── */

function codeBlock(text) {
  const pre = h('pre', {}, h('code', {}, text));
  const btn = h('button', { class: 'btn sm', onclick: () => copyText(text, btn) }, 'Copy');
  return h('div', { class: 'rel' }, h('div', { class: 'abs-tr' }, btn), pre);
}

function logView(entries, empty) {
  if (!entries.length) return h('div', { class: 'log' }, h('div', { class: 'empty' }, empty));
  return h('div', { class: 'log' }, entries.map((e) => h('div', { class: `log-entry ${e.kind}` },
    h('div', { class: 'meta' }, h('span', { class: 'tag' }, e.tag || (e.kind === 'ok' ? '●' : e.kind === 'error' ? '✕' : 'ℹ')), h('span', {}, e.time), e.extra && h('span', {}, e.extra)),
    typeof e.data === 'string' ? e.data : pretty(e.data))));
}

function rpcTryIt(o) {
  const hist = () => state.rpcHistory.get(o.id) || [];
  const initial = state.drafts.get(o.id) ?? pretty(example(o.request?.payload) ?? {});
  const editor = h('textarea', { class: 'editor', spellcheck: 'false', value: initial,
    oninput: (e) => { state.drafts.set(o.id, e.target.value); e.target.classList.remove('invalid'); errEl.textContent = ''; },
    onkeydown: (e) => { if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); send(); } } });
  const errEl = h('div', { class: 'err-text' });
  const out = h('div', {});
  const sendBtn = h('button', { class: 'btn primary', onclick: () => send() }, 'Send request');
  const draw = () => { out.replaceChildren(logView(hist(), 'Responses appear here')); };

  async function send() {
    let payload;
    try { payload = editor.value.trim() ? JSON.parse(editor.value) : undefined; }
    catch (e) { editor.classList.add('invalid'); errEl.textContent = `Invalid JSON: ${e.message}`; return; }
    sendBtn.disabled = true; sendBtn.textContent = 'Sending…';
    const t0 = performance.now();
    const push = (e) => { state.rpcHistory.set(o.id, [{ time: clock(), ...e }, ...hist()].slice(0, 30)); draw(); };
    try {
      await ensureConnected();
      const data = await client.rpc(o.address, payload);
      push({ kind: 'ok', tag: 'OK', data, extra: `${Math.round(performance.now() - t0)} ms` });
    } catch (e) {
      push({ kind: 'error', tag: e.code != null ? `ERROR ${e.code}` : 'ERROR', data: e.message, extra: e.temporary ? 'temporary' : '' });
    } finally { sendBtn.disabled = false; sendBtn.textContent = 'Send request'; }
  }
  draw();
  return h('div', { class: 'card' },
    h('div', { class: 'head' }, h('span', {}, 'Try it'), h('span', { class: 'pill' }, o.address), h('span', { class: 'spacer' }),
      h('button', { class: 'btn sm', onclick: () => { state.drafts.delete(o.id); editor.value = pretty(example(o.request?.payload) ?? {}); } }, 'Reset')),
    h('div', { class: 'content' }, editor, errEl,
      h('div', { class: 'row my10' }, sendBtn, h('span', { class: 'muted' }, client.connected ? 'Ctrl+Enter to send' : 'Will connect first')),
      h('div', { class: 'row mb6 sb' }, h('b', {}, 'Responses'),
        h('button', { class: 'btn sm', onclick: () => { state.rpcHistory.delete(o.id); draw(); } }, 'Clear')),
      out));
}

function channelTryIt(o) {
  const ch = o.address;
  const out = h('div', {});
  const actions = h('div', { class: 'row mb10' });
  const note = h('div', { class: 'err-text' });
  const draw = () => {
    const sub = client.subs.has(ch);
    actions.replaceChildren(
      sub ? h('button', { class: 'btn danger', onclick: unsub }, 'Unsubscribe') : h('button', { class: 'btn primary', onclick: subscribe }, 'Subscribe'),
      h('button', { class: 'btn', onclick: () => { state.paused.has(ch) ? state.paused.delete(ch) : state.paused.add(ch); draw(); } }, state.paused.has(ch) ? 'Resume feed' : 'Pause feed'),
      h('button', { class: 'btn', onclick: () => { state.feeds.delete(ch); draw(); } }, 'Clear'),
      h('span', { class: `badge ${sub ? 'pub' : 'neutral'}` }, sub ? '● live' : '○ offline'));
    out.replaceChildren(logView(state.feeds.get(ch) || [], sub ? 'Waiting for messages…' : 'Subscribe to start receiving messages'));
  };
  async function subscribe() {
    note.textContent = '';
    try { await ensureConnected(); await client.subscribe(ch); renderNav(); }
    catch (e) { note.textContent = e.code != null ? `Subscribe refused (${e.code}): ${e.message}` : e.message; }
    draw();
  }
  async function unsub() {
    try { await client.unsubscribe(ch); } catch (e) { note.textContent = e.message; }
    renderNav(); draw();
  }
  refreshLive = (c) => { if (c === undefined || c === ch) draw(); };
  draw();
  return h('div', { class: 'card' },
    h('div', { class: 'head' }, h('span', {}, 'Try it'), h('span', { class: 'pill' }, ch)),
    h('div', { class: 'content' }, actions, note, out));
}

function snippetsCard(o) {
  const payload = state.drafts.get(o.id) ?? pretty(example(o.request?.payload) ?? {});
  let body;
  try { body = pretty(JSON.parse(payload)); } catch { body = payload; }
  const url = connUrl || 'wss://host/connection/websocket';
  const indent = (s, n) => s.split('\n').map((l, i) => (i ? ' '.repeat(n) + l : l)).join('\n');
  const rpc = o.kind === 'rpc';
  const snippets = {
    'centrifuge-js': rpc
? `import { Centrifuge } from 'centrifuge';

const client = new Centrifuge('${url}', { token: '<JWT>' });
client.connect();
await client.ready();

const res = await client.rpc('${o.address}', ${indent(body, 0)});
console.log(res.data);`
: `import { Centrifuge } from 'centrifuge';

const client = new Centrifuge('${url}', { token: '<JWT>' });
const sub = client.newSubscription('${o.address}');
sub.on('publication', (ctx) => console.log(ctx.data));
sub.subscribe();
client.connect();`,
    'WebSocket': `const ws = new WebSocket('${url}');
ws.onopen = () => {
  ws.send(JSON.stringify({ id: 1, connect: { token: '<JWT>' } }));
  ${rpc ? `ws.send(JSON.stringify({ id: 2, rpc: { method: '${o.address}', data: ${indent(body, 2)} } }));`
        : `ws.send(JSON.stringify({ id: 2, subscribe: { channel: '${o.address}' } }));`}
};
ws.onmessage = (e) => {
  for (const line of e.data.split('\\n')) {
    const msg = JSON.parse(line);
    if (Object.keys(msg).length === 0) return ws.send('{}'); // ping → pong
    console.log(msg);
  }
};`,
  };
  const holder = h('div', {});
  const tabs = h('div', { class: 'tabs' });
  const draw = () => {
    if (!snippets[state.snippetTab]) state.snippetTab = 'centrifuge-js';
    tabs.replaceChildren(...Object.keys(snippets).map((k) => h('button', { class: `tab ${k === state.snippetTab ? 'active' : ''}`, onclick: () => { state.snippetTab = k; draw(); } }, k)));
    holder.replaceChildren(codeBlock(snippets[state.snippetTab]));
  };
  draw();
  return h('div', { class: 'card' }, h('div', { class: 'head' }, h('span', {}, 'Code'), h('span', { class: 'grow' }), tabs), h('div', { class: 'content' }, holder));
}

/* ───────────────────────── boot ───────────────────────── */

function applyTheme(t) {
  document.documentElement.dataset.theme = t;
  store.set(localStorage, 'fuge.theme', t);
}

async function boot() {
  const app = document.getElementById('app');
  try {
    const res = await fetch(CFG.specJson, { headers: { Accept: 'application/json' } });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    spec = await res.json();
  } catch (e) {
    app.replaceChildren(h('p', { class: 'boot' }, `Could not load ${CFG.specJson}: ${e.message}`));
    return;
  }
  state.model = buildModel();
  document.title = `${spec.info?.title || 'AsyncAPI'} Reference`;
  connUrl = defaultWsUrl();
  window.FugeAuth?.init({ spec, h, onChange: renderConn });
  applyTheme(store.get(localStorage, 'fuge.theme') || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'));

  app.replaceChildren(h('div', { class: 'shell' },
    h('header', { class: 'topbar' },
      h('button', { class: 'btn icon-btn menu-btn', 'aria-label': 'Toggle navigation', onclick: () => setNav(!document.querySelector('.shell').classList.contains('nav-open')) }, '☰'),
      h('div', { class: 'brand' }, spec.info?.title || 'AsyncAPI', spec.info?.version && h('small', {}, `v${spec.info.version}`)),
      h('div', { class: 'conn', id: 'conn' }),
      h('button', { class: 'btn icon-btn', title: 'Toggle theme', onclick: () => applyTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark') }, '◐')),
    h('div', { class: 'body' },
      h('aside', { class: 'sidebar' },
        h('input', { class: 'search', placeholder: 'Search…', 'aria-label': 'Search', oninput: (e) => { state.search = e.target.value; renderNav(); } }),
        h('nav', { id: 'nav' })),
      h('div', { class: 'backdrop', onclick: () => setNav(false) }),
      h('main', { class: 'main', id: 'main' }))));
  window.addEventListener('keydown', (e) => { if (e.key === 'Escape') setNav(false); });
  renderConn();
  window.addEventListener('hashchange', route);
  route();
}
boot();
})();
