// Browser-driven test for the inline corrector (internal/serve/layout.html).
//
// The Go tests can assert the markup is on the page and that /-/correct behaves;
// they cannot select text. This drives real selections and real mouse events in
// headless Chrome over the DevTools protocol, through both branches: the
// paste-able handoff (no worker) and the queued job whose report the browser
// polls for. It is deliberately outside `mage check` — it needs node and a local
// Chrome, neither of which the Go build depends on. Run it with `mage e2e`.
//
// Hermetic: it serves a throwaway tutorial from a temp HOME and drives a temp
// Chrome profile, so it never touches the reader's ~/.lathe or their browser.
// The queued branch fakes the worker with a long-poll on /-/work and a
// `work answer` POST, so no part file is ever rewritten.
//
// Env: LATHE_BIN (default ./lathe), CHROME (default: the usual local paths).

import {spawn} from 'node:child_process';
import {mkdtemp, mkdir, writeFile, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {createServer} from 'node:net';
import {join} from 'node:path';
import {existsSync} from 'node:fs';

const LATHE = process.env.LATHE_BIN || './lathe';
const SLUG = 'e2e-corrector';
const PART = 'part-01.md';
const PARAGRAPH =
  'The apply trace is written to stdout, one JSON object per line, so a ' +
  'downstream tool can consume it without parsing the human-readable log.';

const sleep = ms => new Promise(r => setTimeout(r, ms));

function chromePath() {
  const candidates = [
    process.env.CHROME,
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    '/Applications/Chromium.app/Contents/MacOS/Chromium',
    '/usr/bin/google-chrome',
    '/usr/bin/chromium',
    '/usr/bin/chromium-browser',
  ].filter(Boolean);
  const found = candidates.find(p => existsSync(p));
  if (!found) {
    console.error('no Chrome found — set CHROME=/path/to/chrome');
    process.exit(2);
  }
  return found;
}

function freePort() {
  return new Promise(res => {
    const s = createServer();
    s.listen(0, '127.0.0.1', () => {
      const {port} = s.address();
      s.close(() => res(port));
    });
  });
}

// --- a throwaway tutorial in a throwaway HOME ---------------------------------

const home = await mkdtemp(join(tmpdir(), 'lathe-e2e-'));
const tutDir = join(home, '.lathe', 'tutorials', SLUG);
await mkdir(tutDir, {recursive: true});
await writeFile(join(tutDir, PART), `# Reading the apply trace\n\n${PARAGRAPH}\n`);
await writeFile(join(tutDir, 'metadata.json'), JSON.stringify({
  slug: SLUG,
  title: 'Corrector E2E',
  topic: 'testing',
  created: new Date().toISOString(),
  status: 'verified',
  parts: [PART],
}, null, 2));

const port = await freePort();
const base = `http://127.0.0.1:${port}`;
const dbgPort = await freePort();
const profile = join(home, 'chrome-profile');

const serve = spawn(LATHE, ['serve', '--port', String(port), '--no-open'], {
  env: {...process.env, HOME: home},
  stdio: 'ignore',
});
const chrome = spawn(chromePath(), [
  '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
  `--remote-debugging-port=${dbgPort}`, `--user-data-dir=${profile}`, 'about:blank',
], {stdio: 'ignore'});

// Chrome keeps writing to its profile for a moment after SIGTERM, so give it
// one, then retry the removal rather than failing the run on an ENOTEMPTY.
const cleanup = async () => {
  serve.kill();
  chrome.kill();
  await sleep(300);
  await rm(home, {recursive: true, force: true, maxRetries: 10, retryDelay: 200}).catch(() => {});
};
process.on('exit', () => { serve.kill(); chrome.kill(); });

// --- CDP plumbing -------------------------------------------------------------

let ws, wsId = 0;
const pending = new Map();
const consoleErrors = [];

function send(method, params = {}) {
  const id = ++wsId;
  ws.send(JSON.stringify({id, method, params}));
  return new Promise((res, rej) => pending.set(id, {res, rej}));
}
async function evaluate(expression) {
  const r = await send('Runtime.evaluate', {expression, returnByValue: true, awaitPromise: true});
  if (r.exceptionDetails) {
    throw new Error('JS: ' + (r.exceptionDetails.exception?.description || r.exceptionDetails.text));
  }
  return r.result.value;
}

const checks = [];
function check(name, ok, extra = '') {
  checks.push({name, ok});
  console.log((ok ? 'PASS  ' : 'FAIL  ') + name + (extra ? '  — ' + extra : ''));
}

async function waitForServer() {
  for (let i = 0; i < 60; i++) {
    try { if ((await fetch(base + '/')).ok) return; } catch {}
    await sleep(250);
  }
  throw new Error('lathe serve did not come up on ' + base);
}

async function attach() {
  for (let i = 0; i < 60; i++) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${dbgPort}/json/list`)).json();
      const target = list.find(t => t.type === 'page' && t.webSocketDebuggerUrl);
      if (target) {
        ws = new WebSocket(target.webSocketDebuggerUrl);
        ws.onmessage = ev => {
          const m = JSON.parse(ev.data);
          if (m.id && pending.has(m.id)) {
            const {res, rej} = pending.get(m.id);
            pending.delete(m.id);
            m.error ? rej(new Error(JSON.stringify(m.error))) : res(m.result);
          } else if (m.method === 'Runtime.exceptionThrown') {
            consoleErrors.push(m.params.exceptionDetails.exception?.description || m.params.exceptionDetails.text);
          } else if (m.method === 'Runtime.consoleAPICalled' && m.params.type === 'error') {
            consoleErrors.push(m.params.args.map(a => a.value ?? a.description).join(' '));
          }
        };
        await new Promise(r => ws.onopen = r);
        await send('Runtime.enable');
        await send('Page.enable');
        return;
      }
    } catch {}
    await sleep(250);
  }
  throw new Error('no Chrome page target on port ' + dbgPort);
}

// --- page interactions --------------------------------------------------------

// Select a run of prose and end the drag with a real mouseup, the way a reader
// does. Skips the voice-reveal block, whose text is present but not rendered.
const SELECT = `(async () => {
  const art = document.querySelector('article') || document.querySelector('main');
  const p = [...art.querySelectorAll('p')].find(el =>
    !el.closest('.voice-reveal-body') && el.getBoundingClientRect().height > 0 && el.textContent.trim().length > 60);
  if (!p) return null;
  p.scrollIntoView({block: 'center'});
  await new Promise(r => setTimeout(r, 250));
  const n = [...p.childNodes].find(c => c.nodeType === 3 && c.nodeValue.trim().length > 40);
  if (!n) return null;
  const r = document.createRange();
  r.setStart(n, 0); r.setEnd(n, Math.min(60, n.nodeValue.length));
  const sel = getSelection(); sel.removeAllRanges(); sel.addRange(r);
  document.dispatchEvent(new MouseEvent('mouseup', {bubbles: true}));
  await new Promise(r => setTimeout(r, 100));
  return sel.toString().trim();
})()`;

const POPUP = `(() => {
  const p = document.getElementById('correctPopup');
  const s = document.getElementById('correctStatus');
  return {hidden: p.hidden, left: p.style.left, top: p.style.top,
          status: s.hidden ? '' : s.textContent,
          buttons: [...s.querySelectorAll('button')].map(b => b.textContent),
          pre: s.querySelector('pre') ? s.querySelector('pre').textContent : ''};
})()`;

const CLICK_OUTSIDE = `document.body.dispatchEvent(new MouseEvent('mousedown', {bubbles: true})), true`;
const SCROLL = `window.dispatchEvent(new Event('scroll')), true`;
const ESCAPE = `document.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true})), true`;

async function submitNote(note) {
  await evaluate(`(() => {
    document.getElementById('correctInput').value = ${JSON.stringify(note)};
    document.getElementById('correctForm').dispatchEvent(new Event('submit', {bubbles: true, cancelable: true}));
    return true;
  })()`);
}
async function waitFor(pred, ms = 15000) {
  const t0 = Date.now();
  let st;
  while (Date.now() - t0 < ms) {
    st = await evaluate(POPUP);
    if (pred(st)) return st;
    await sleep(200);
  }
  return st;
}
async function goto(url) {
  await send('Page.navigate', {url});
  for (let i = 0; i < 60; i++) {
    if (await evaluate('document.readyState').catch(() => null) === 'complete'
        && await evaluate(`!!document.getElementById('correctPopup')`)) return;
    await sleep(200);
  }
  throw new Error('page did not load: ' + url);
}

// --- the run ------------------------------------------------------------------

try {
  await waitForServer();
  await attach();
  await goto(`${base}/${SLUG}/${PART}`);

  // Branch 1: no worker connected → paste-able handoff.
  const selected = await evaluate(SELECT);
  check('selection inside the article arms the popup', !!selected, JSON.stringify(selected?.slice(0, 40) ?? null));
  let st = await evaluate(POPUP);
  check('popup is visible and positioned', st.hidden === false && !!st.left && !!st.top, `left=${st.left} top=${st.top}`);

  await evaluate(SCROLL);
  st = await evaluate(POPUP);
  check('scroll dismisses an idle popup', st.hidden === true);

  await evaluate(SELECT);
  await submitNote('it is stderr, not stdout');
  st = await waitFor(s => !!s.pre || (s.status || '').startsWith('Error'));
  check('handoff block is rendered with the sentinels',
    st.pre.startsWith('/lathe-correct ') && st.pre.includes('<<<') && st.pre.includes('>>>'),
    JSON.stringify(st.pre.slice(0, 60)));
  check('handoff offers Copy', st.buttons.includes('Copy'));
  check('window.latheCopyText is reachable', await evaluate(`typeof window.latheCopyText === 'function'`));

  await evaluate(SCROLL);
  st = await evaluate(POPUP);
  check('scroll keeps a resolved handoff on screen', st.hidden === false && !!st.pre);

  await evaluate(CLICK_OUTSIDE);
  st = await evaluate(POPUP);
  check('a click outside dismisses the handoff', st.hidden === true);

  const again = await evaluate(SELECT);
  st = await evaluate(POPUP);
  check('the popup re-arms after dismissal', !!again && st.hidden === false);

  await submitNote('a second note');
  await waitFor(s => !!s.pre);
  await evaluate(SELECT);
  st = await evaluate(POPUP);
  check('a fresh selection supersedes a shown result', st.hidden === false && !st.pre && !st.status);
  await evaluate(ESCAPE);

  // Branch 2: worker connected → queued job, then its report.
  // A long-poll on /-/work is exactly what `lathe work next` does, so it marks a
  // worker present and claims the job the browser enqueues.
  let claimed = null;
  const workerLoop = (async () => {
    for (let i = 0; i < 4 && !claimed; i++) {
      const r = await fetch(base + '/-/work');
      if (r.status === 200) { claimed = await r.json(); return; }
    }
  })();
  await sleep(600);

  await evaluate(SELECT);
  await submitNote('the sample rate should be 1024');
  st = await waitFor(s => (s.status || '').startsWith('Applying') || !!s.pre);
  check('a queued job shows the Applying pill', (s => s.startsWith('Applying'))(st.status || ''), JSON.stringify(st.status));

  await evaluate(SCROLL);
  await evaluate(CLICK_OUTSIDE);
  st = await evaluate(POPUP);
  check('an in-flight job survives scroll and a click outside',
    st.hidden === false && (st.status || '').startsWith('Applying'));

  await Promise.race([workerLoop, sleep(20000)]);
  check('the worker claimed a correct job carrying the excerpt and note',
    !!claimed && claimed.type === 'correct' && !!claimed.excerpt && !!claimed.note,
    claimed ? `part=${claimed.part} note=${JSON.stringify(claimed.note)}` : 'none');

  if (claimed) {
    const report = 'left unchanged: the tutorial is right here';
    const r = await fetch(`${base}/-/work/${claimed.id}/answer`, {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({answer: report}),
    });
    check('work answer closes the correct job', r.status === 204, 'HTTP ' + r.status);
    st = await waitFor(s => (s.status || '').startsWith(report), 12000);
    check("the worker's report is rendered verbatim", (st.status || '').startsWith(report), JSON.stringify(st.status));
    check('the report offers Reload', st.buttons.includes('Reload'));
    await evaluate(CLICK_OUTSIDE);
    st = await evaluate(POPUP);
    check('a click outside dismisses the report', st.hidden === true);
  }

  // Not the corrector, but the same page and the same harness: triggerSave once
  // referenced an undeclared `exercises`, so every progress save threw before
  // its fetch. A ReferenceError in another script block is invisible to the Go
  // tests and to the reader; it is not invisible here.
  await goto(`${base}/${SLUG}/${PART}`);
  const saved = await evaluate(`(async () => {
    document.getElementById('saveProgressButton').click();
    for (let i = 0; i < 40; i++) {
      const t = document.getElementById('progressStatus').textContent;
      if (t) return t;
      await new Promise(r => setTimeout(r, 100));
    }
    return '';
  })()`);
  check('the Save progress button reaches the server', saved === 'Progress saved', JSON.stringify(saved));

  check('no uncaught JS errors', consoleErrors.filter(e => !/mermaid/i.test(e)).length === 0, consoleErrors.join(' | '));
} finally {
  await cleanup();
}

const failed = checks.filter(c => !c.ok).length;
console.log(`\n${checks.length - failed}/${checks.length} checks passed`);
process.exit(failed ? 1 : 0);
