#!/usr/bin/env node
/**
 * tools/diff/shots.mjs — deterministic page-by-page screenshots of a running
 * reference server, used to produce the visual "before" baseline.
 *
 * Usage
 *   node tools/diff/shots.mjs <base-url> <output-dir>
 *   node tools/diff/shots.mjs http://127.0.0.1:8010 docs/baseline/screenshots
 *
 * Env
 *   SHOT_ADMIN_USER      admin username        (default "admin")
 *   SHOT_ADMIN_PASSWORD  admin password        (default "DiffAdmin123!@#")
 *   SHOT_SETTLE_MS       extra settle delay per page, ms (default 800; the
 *                        workspace/console SPAs always add their documented 2000ms)
 *
 * Output
 *   One PNG per page named "<name>.png" (no timestamps), viewport screenshots
 *   (full-page OFF) at 1280x900, device scale factor 1.
 *   stdout: one line per captured page, "<name> <http status> <file>".
 *   stderr: diagnostics, warnings and failures.
 *   <outDir>/shots.log always records the same capture lines.
 *   Inside a kernel, pass `onLine`/`onLog` (e.g. nodeRepl.write) to see them, or
 *   read the lines from the returned results (`results[i].line`).
 *
 * Browser access
 *   Capture drives the official ZCode Browser Use runtime (the same
 *   `agent.browsers` object the `control-browser` skill bootstraps). That
 *   runtime only exists inside a ZCode node_repl kernel in *main* runtime scope,
 *   and an imported ES module runs in its own realm — it cannot see the kernel's
 *   globals — so the browser object has to be injected explicitly:
 *
 *     // main-scope ZCode agent, `mcp__node_repl__js` call:
 *     const root = process.env.ZCODE_PLUGIN_ROOT;
 *     const { join } = await import("node:path");
 *     const { pathToFileURL } = await import("node:url");
 *     const { setupBrowserRuntime } = await import(
 *       pathToFileURL(join(root, "scripts", "browser-client.mjs")).href);
 *     await setupBrowserRuntime({ globals: globalThis });          // installs agent.browsers
 *     const shots = await import(
 *       pathToFileURL("/Users/yoahoug/Desktop/mirror-studio/tools/diff/shots.mjs").href);
 *     await shots.captureAll({
 *       browsers: agent.browsers,                                   // ← required in the kernel
 *       baseUrl: "http://127.0.0.1:8010",
 *       outDir: "/Users/yoahoug/Desktop/mirror-studio/docs/baseline/screenshots",
 *       onLine: (line) => nodeRepl.write(line + "\n"),              // optional
 *     });
 *
 *   `captureAll()` also accepts `browsers` from the caller; without it, it reuses
 *   `globalThis.agent.browsers` and finally bootstraps the bridge itself from
 *   ZCODE_PLUGIN_ROOT (the plain `node tools/diff/shots.mjs ...` CLI path, which
 *   needs a process that already carries the browser bridge). Subagent runtime
 *   scope is refused by the host ("Browser is not available in subagent"), so
 *   run the capture from a main-scope session.
 */

import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

export const DEFAULT_ADMIN_USER = "admin";
export const DEFAULT_ADMIN_PASSWORD = "DiffAdmin123!@#";
export const VIEWPORT = Object.freeze({ width: 1280, height: 900 });
export const BOOT_SPINNER_TEXT = "正在打开创作台";
export const DEFAULT_SETTLE_MS = 800;
export const SPA_SETTLE_MS = 2000;

/**
 * name -> path, in capture order. Anonymous pages are captured first, then the
 * admin logs in once (session cookie), then the authenticated pages. The
 * workspace SPA reads auth state from server-rendered data attributes, so the
 * login happens before it is visited and the page is reloaded right after login.
 */
export const PAGES = Object.freeze([
  { name: "home", path: "/", auth: false, settle: "generic" },
  { name: "pricing", path: "/pricing", auth: false, settle: "generic" },
  { name: "login", path: "/login", auth: false, settle: "generic" },
  { name: "register", path: "/register", auth: false, settle: "generic" },
  { name: "forgot-password", path: "/forgot-password", auth: false, settle: "generic" },
  { name: "account", path: "/account", auth: true, settle: "generic" },
  { name: "account-security", path: "/account/security", auth: true, settle: "generic" },
  { name: "account-history", path: "/account/history", auth: true, settle: "generic" },
  // React SPA shell: wait for the app to render (login/registration modal or the
  // workbench UI) and for the boot spinner to disappear, then 2 more seconds.
  { name: "workspace", path: "/workspace", auth: true, settle: "workspace" },
  { name: "console", path: "/console/", auth: true, settle: "console" },
  { name: "public-error", path: "/this-page-does-not-exist", auth: true, settle: "generic" },
]);

/**
 * Output sinks. A plain CLI run writes the capture lines to stdout and
 * diagnostics to stderr; when the module is imported into a ZCode kernel (whose
 * module realm owns no CLI streams) the caller should pass `onLine`/`onLog`,
 * otherwise the lines are collected, returned in the results and written to
 * <outDir>/shots.log.
 */
const isCliRun = (() => {
  try {
    const entry = process.argv[1];
    return Boolean(entry) && pathToFileURL(resolve(entry)).href === import.meta.url;
  } catch {
    return false;
  }
})();

let sinks = {
  out: (line) => (isCliRun ? process.stdout.write(`${line}\n`) : console.log(line)),
  log: (message) =>
    isCliRun ? process.stderr.write(`shots: ${message}\n`) : console.log(`shots: ${message}`),
};

function setSinks(next = {}) {
  const previous = sinks;
  if (typeof next.onLine === "function") sinks.out = next.onLine;
  if (typeof next.onLog === "function") sinks.log = next.onLog;
  return () => {
    sinks = previous;
  };
}

const log = (msg) => sinks.log(String(msg));
const toAbs = (url, base) => new URL(url, base).toString();

function usage(stream = null) {
  const text = [
    "usage: node tools/diff/shots.mjs <base-url> <output-dir>",
    "  e.g. node tools/diff/shots.mjs http://127.0.0.1:8010 docs/baseline/screenshots",
    "",
    "env: SHOT_ADMIN_USER (default admin), SHOT_ADMIN_PASSWORD (default " +
      DEFAULT_ADMIN_PASSWORD +
      ")",
  ].join("\n");
  if (stream && typeof stream.write === "function") stream.write(`${text}\n`);
  else for (const line of text.split("\n")) sinks.log(line);
}

/* ------------------------------------------------------------------ browser */

/**
 * Returns the browser registry. Precedence: an explicitly injected
 * `browsers` object (required when the script is imported into a ZCode kernel,
 * because module realms do not share globals), then a bridge the caller
 * bootstrapped on this realm, then bootstrapping the official browser-client
 * from ZCODE_PLUGIN_ROOT.
 */
export async function getBrowsers(injected) {
  if (injected) return injected;
  if (globalThis.agent?.browsers) return globalThis.agent.browsers;
  const pluginRoot = process.env.ZCODE_PLUGIN_ROOT || process.env.CLAUDE_PLUGIN_ROOT;
  if (!pluginRoot) {
    throw new Error(
      "no browser registry: pass { browsers: agent.browsers } from the ZCode kernel " +
        "(or set ZCODE_PLUGIN_ROOT so the official browser-client can be loaded).",
    );
  }
  const { setupBrowserRuntime } = await import(
    pathToFileURL(join(pluginRoot, "scripts", "browser-client.mjs")).href
  );
  try {
    await setupBrowserRuntime({ globals: globalThis });
  } catch (error) {
    throw new Error(
      `browser runtime unavailable: ${error?.message ?? error}. ` +
        "Bootstrap the control-browser skill in a main-scope ZCode session and pass " +
        "the resulting agent.browsers to captureAll({ browsers }).",
    );
  }
  if (!globalThis.agent?.browsers) throw new Error("browser runtime did not expose agent.browsers");
  return globalThis.agent.browsers;
}

/* -------------------------------------------------------------- page helpers */

/** Fixed wait that respects the 3000ms per-call budget. */
async function sleep(tab, ms) {
  let left = Math.max(0, Math.floor(ms));
  while (left > 0) {
    const step = Math.min(left, 1500);
    await tab.playwright.waitForTimeout(step);
    left -= step;
  }
}

/** Polls a page-side boolean expression until it is true or the budget runs out. */
async function pollFor(tab, expression, { timeoutMs = 15000, intervalMs = 300, label = "condition" } = {}) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    try {
      if (await tab.playwright.evaluate(expression)) return true;
    } catch (error) {
      log(`poll(${label}) error: ${error?.message ?? error}`);
    }
    if (Date.now() >= deadline) return false;
    await tab.playwright.waitForTimeout(intervalMs);
  }
}

const READY_EXPR = `(() => {
  const d = document;
  if (!d || d.readyState !== 'complete') return false;
  if (d.fonts && d.fonts.status && d.fonts.status !== 'loaded') return false;
  const images = Array.from(d.images || []);
  if (images.some((img) => !img.complete)) return false;
  return true;
})()`;

const DOM_SIGNATURE_EXPR = `(() => {
  const b = document.body;
  if (!b) return null;
  return String(b.innerText.length) + ':' + String(b.getElementsByTagName('*').length);
})()`;

/** Waits until the DOM signature (text length + node count) stops changing. */
async function waitForStableDom(tab, { timeoutMs = 8000, intervalMs = 300, requiredStable = 3 } = {}) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  let stable = 0;
  for (;;) {
    let signature = null;
    try {
      signature = await tab.playwright.evaluate(DOM_SIGNATURE_EXPR);
    } catch {
      signature = null;
    }
    if (signature !== null && signature === last) {
      stable += 1;
      if (stable >= requiredStable) return true;
    } else {
      stable = 0;
    }
    last = signature;
    if (Date.now() >= deadline) return false;
    await tab.playwright.waitForTimeout(intervalMs);
  }
}

/** Generic settle: document complete + fonts + images, DOM quiet, small pause. */
async function settleGeneric(tab, { settleMs = DEFAULT_SETTLE_MS } = {}) {
  await pollFor(tab, READY_EXPR, { timeoutMs: 10000, label: "document ready" });
  await waitForStableDom(tab, { timeoutMs: 6000 });
  await sleep(tab, settleMs);
}

/** The workspace SPA boot spinner must be gone and either the auth modal or the
 * workbench UI must be visible; then wait SPA_SETTLE_MS more. */
const WORKSPACE_READY_EXPR = `(() => {
  if (!document.body) return false;
  if (document.readyState !== 'complete') return false;
  const text = document.body.innerText || '';
  if (text.includes(${JSON.stringify(BOOT_SPINNER_TEXT)})) return false;
  const visible = (el) => {
    const rect = el.getBoundingClientRect();
    const style = getComputedStyle(el);
    return rect.width > 0 && rect.height > 0 && style.display !== 'none' && style.visibility !== 'hidden';
  };
  const controls = Array.from(
    document.querySelectorAll('[role="dialog"], dialog, form, input, textarea, button'),
  ).some(visible);
  const root = document.getElementById('workspace-root');
  const rendered = root
    ? Array.from(root.children).some((child) => !String(child.className || '').includes('workspace-boot'))
    : false;
  return controls || rendered;
})()`;

async function settleWorkspace(tab) {
  const ready = await pollFor(tab, WORKSPACE_READY_EXPR, { timeoutMs: 25000, label: "workspace ui" });
  if (!ready) log("workspace: boot spinner/UI condition not met within 25s, capturing anyway");
  await sleep(tab, SPA_SETTLE_MS);
  return ready;
}

/** The console SPA must have rendered inside #console-root and show no loading state. */
const CONSOLE_READY_EXPR = `(() => {
  if (document.readyState !== 'complete') return false;
  const root = document.getElementById('console-root');
  if (!root) return false;
  if (root.getElementsByTagName('*').length < 3) return false;
  const text = document.body ? (document.body.innerText || '') : '';
  if (/(加载中|正在加载|loading)/i.test(text)) return false;
  return true;
})()`;

async function settleConsole(tab) {
  const ready = await pollFor(tab, CONSOLE_READY_EXPR, { timeoutMs: 25000, label: "console ui" });
  if (!ready) log("console: UI condition not met within 25s, capturing anyway");
  await waitForStableDom(tab, { timeoutMs: 6000 });
  await sleep(tab, SPA_SETTLE_MS);
  return ready;
}

/* --------------------------------------------------------------------- login */

/**
 * Logs in with the seeded admin credentials using page fetch() on the base
 * origin: GET /api/auth/csrf -> GET /api/auth/captcha -> POST /api/auth/login.
 * Fails loudly when the login response is not 200/code 0.
 */
export async function login(tab, { baseUrl, user, password }) {
  const payload = JSON.stringify({ user, password });
  const expression = `(async () => {
    const { user, password } = ${payload};
    const out = { steps: {} };
    const getJson = async (url) => {
      const response = await fetch(url, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
      const text = await response.text();
      let json = null;
      try { json = JSON.parse(text); } catch (error) { json = null; }
      return { status: response.status, ok: response.ok, json, text: text.slice(0, 300) };
    };

    const csrf = await getJson('/api/auth/csrf');
    out.steps.csrf = csrf.status;
    const csrfToken =
      (csrf.json && (csrf.json.csrf_token || (csrf.json.data && csrf.json.data.csrf_token))) || null;
    if (!csrf.ok || !csrfToken) {
      return { ...out, fatal: 'csrf', detail: csrf.text };
    }

    const captcha = await getJson('/api/auth/captcha');
    out.steps.captcha = captcha.status;
    const captchaCode =
      (captcha.json && captcha.json.data && captcha.json.data.captcha_code) ||
      (captcha.json && captcha.json.captcha_code) ||
      null;
    if (!captcha.ok || !captchaCode) {
      return { ...out, fatal: 'captcha', detail: captcha.text };
    }

    const response = await fetch('/api/auth/login', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRFToken': csrfToken },
      body: JSON.stringify({ username: user, password: password, captcha_code: captchaCode }),
    });
    const text = await response.text();
    let body = null;
    try { body = JSON.parse(text); } catch (error) { body = null; }
    out.steps.login = response.status;
    return {
      ...out,
      status: response.status,
      code: body ? body.code : null,
      message: body ? body.message : null,
      body: body || text.slice(0, 300),
    };
  })()`;

  const result = await tab.playwright.evaluate(expression);
  const ok = result && result.status === 200 && result.code === 0;
  if (!ok) {
    const detail = JSON.stringify(result);
    throw new Error(
      `FATAL: admin login failed for user '${user}' via ${baseUrl} (need HTTP 200 and code 0): ${detail}`,
    );
  }
  log(`login ok: ${baseUrl}/api/auth/login -> 200 code 0`);
  return result;
}

/* ------------------------------------------------------------------- capture */

/** Reads the HTTP status of the current document (Navigation Timing, then fetch). */
async function documentStatus(tab, url) {
  const navStatus = await tab
    .playwright.evaluate(
      `(() => {
        try {
          const entry = performance.getEntriesByType('navigation')[0];
          if (entry && typeof entry.responseStatus === 'number' && entry.responseStatus > 0) {
            return entry.responseStatus;
          }
        } catch (error) {}
        return null;
      })()`,
    )
    .catch(() => null);
  if (typeof navStatus === "number" && navStatus > 0) return navStatus;
  const fetched = await tab
    .playwright.evaluate(
      `fetch(${JSON.stringify(url)}, { credentials: 'same-origin' }).then((r) => r.status).catch(() => null)`,
    )
    .catch(() => null);
  return typeof fetched === "number" ? fetched : "?";
}

/** PNG dimensions from the IHDR chunk, used to verify viewport-sized output. */
export function pngSize(bytes) {
  if (!bytes || bytes.length < 24) return null;
  const width = (bytes[16] << 24) | (bytes[17] << 16) | (bytes[18] << 8) | bytes[19];
  const height = (bytes[20] << 24) | (bytes[21] << 16) | (bytes[22] << 8) | bytes[23];
  if (width <= 0 || height <= 0) return null;
  return { width, height };
}

function toPngBuffer(bytes) {
  if (!bytes) throw new Error("screenshot returned no data");
  if (Buffer.isBuffer(bytes)) return bytes;
  // ArrayBuffer.isView / the tag check also work for values created in another
  // realm (the ZCode kernel), where `instanceof` would fail.
  if (ArrayBuffer.isView(bytes)) return Buffer.from(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const tag = Object.prototype.toString.call(bytes);
  if (tag === "[object ArrayBuffer]" || tag === "[object SharedArrayBuffer]") {
    return Buffer.from(bytes);
  }
  if (Array.isArray(bytes)) return Buffer.from(bytes);
  if (typeof bytes === "string") return Buffer.from(bytes, "base64");
  throw new Error(`unsupported screenshot payload: ${tag}`);
}

async function capturePage(tab, page, { baseUrl, outDirArg, outDirAbs, settleMs }) {
  const url = toAbs(page.path, baseUrl);
  await tab.goto(url);
  await tab.playwright.waitForLoadState({ state: "domcontentloaded" });
  await tab.playwright.waitForLoadState({ state: "load" }).catch(() => {});

  const settle = async () => {
    if (page.settle === "workspace") return settleWorkspace(tab);
    if (page.settle === "console") return settleConsole(tab);
    await settleGeneric(tab, { settleMs });
    return true;
  };
  let settled = await settle();

  // SPA overlays (the release-notes dialog, first-run banners) depend on
  // localStorage and would make otherwise identical pages differ between runs.
  // Dismissing them keeps the captured state deterministic for both backends.
  await dismissOverlays(tab);

  const status = await documentStatus(tab, url);
  const finalUrl = await tab.url();
  const fileAbs = join(outDirAbs, `${page.name}.png`);
  // The browser backend occasionally fails to produce a frame even though the
  // page is fully loaded and its DOM carries text: the screenshot then either
  // times out or returns a blank surface. Re-navigate and retry, and never write
  // a frame that is blank while the DOM has text — an unrendered capture must
  // fail loudly instead of being compared as if it were a real difference.
  const bytes = toPngBuffer(await captureFrames(tab, { name: page.name, url, settle }));
  await writeFile(fileAbs, bytes);

  const size = pngSize(bytes);
  if (!size) log(`${page.name}: WARNING unreadable PNG header`);
  else if (size.width !== VIEWPORT.width || size.height !== VIEWPORT.height) {
    log(
      `${page.name}: WARNING image is ${size.width}x${size.height}, expected ` +
        `${VIEWPORT.width}x${VIEWPORT.height} (viewport/DPR override not applied)`,
    );
  }
  if (page.path !== "/" && finalUrl && !finalUrl.replace(/\/$/, "").endsWith(page.path.replace(/\/$/, ""))) {
    log(`${page.name}: NOTE landed on ${finalUrl} (expected ${url})`);
  }
  if (!settled) log(`${page.name}: WARNING page did not meet its settle condition`);

  return { status, file: join(outDirArg, `${page.name}.png`), settled };
}


/**
 * A 1280x900 PNG of a page that actually rendered is far larger than this; a
 * blank surface compresses to a few kilobytes. The threshold is only ever
 * consulted together with "does the DOM have text", so a legitimately empty page
 * is never rejected.
 */
const BLANK_FRAME_BYTES = 20 * 1024;

async function domHasVisibleText(tab) {
  try {
    return await tab.playwright.evaluate(
      `(() => { const body = document.body; return !!body && (body.innerText || '').trim().length > 0; })()`,
    );
  } catch (error) {
    log(`dom text probe failed: ${error?.message ?? error}`);
    return false;
  }
}

async function captureFrames(tab, { name, url, settle, attempts = 3 }) {
  let blank = null;
  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    try {
      const bytes = await tab.screenshot({ fullPage: false });
      if (bytes.length >= BLANK_FRAME_BYTES || !(await domHasVisibleText(tab))) {
        if (attempt > 1) log(`${name}: captured a rendered frame on attempt ${attempt}`);
        return bytes;
      }
      blank = bytes;
      log(
        `${name}: attempt ${attempt} returned a blank frame (${bytes.length} bytes) ` +
          `while the DOM has text; re-navigating and retrying`,
      );
    } catch (error) {
      log(`${name}: screenshot attempt ${attempt} failed (${error?.message ?? error})`);
    }
    if (attempt < attempts) {
      // A retry that reuses the same document keeps hitting the same stuck
      // compositor, so reload the page and let it settle again.
      await tab.goto(url).catch(() => {});
      await tab.playwright.waitForLoadState({ state: "domcontentloaded" }).catch(() => {});
      await settle().catch(() => {});
      await dismissOverlays(tab);
    }
  }
  if (blank) throw new Error(`${name}: every attempt produced a blank frame while the DOM has text`);
  throw new Error(`${name}: no frame could be captured in ${attempts} attempts`);
}

/**
 * Closes known first-run overlays so the captured pixels do not depend on
 * browser storage left over from an earlier run.
 */
async function dismissOverlays(tab) {
  const labels = ["我知道了", "知道了", "确定", "关闭"];
  for (const label of labels) {
    try {
      const button = tab.playwright.getByRole("button", { name: label });
      if ((await button.count()) > 0 && (await button.first().isVisible())) {
        await button.first().click();
        await tab.playwright.waitForTimeout(600);
        return;
      }
    } catch (error) {
      log(`dismissOverlays(${label}) skipped: ${error?.message ?? error}`);
    }
  }
}

/**
 * Visits every page in `pages`, logs in once for the authenticated ones and
 * writes one PNG per page. Returns a per-page result summary.
 */
export async function captureAll(options = {}) {
  const baseUrl = options.baseUrl ?? process.env.SHOT_BASE_URL;
  const outDirArg = options.outDir ?? options.outDirArg ?? process.env.SHOT_OUT_DIR;
  const adminUser = options.adminUser ?? process.env.SHOT_ADMIN_USER ?? DEFAULT_ADMIN_USER;
  const adminPassword =
    options.adminPassword ?? process.env.SHOT_ADMIN_PASSWORD ?? DEFAULT_ADMIN_PASSWORD;
  const settleMs = Number(
    options.settleMs ?? process.env.SHOT_SETTLE_MS ?? DEFAULT_SETTLE_MS,
  );
  const pages = options.pages ?? PAGES;
  if (!baseUrl || !outDirArg) {
    usage();
    throw new Error("both <base-url> and <output-dir> are required");
  }

  const browsers = await getBrowsers(options.browsers);
  const outDirAbs = resolve(outDirArg);
  await mkdir(outDirAbs, { recursive: true });
  const restoreSinks = setSinks(options);
  const lines = [];

  const tab = await browsers.tabs.new();
  const results = [];
  try {
    await tab.setViewportSize({ width: VIEWPORT.width, height: VIEWPORT.height });
    await tab.playwright.waitForLoadState({ state: "domcontentloaded" }).catch(() => {});

    const context = { baseUrl, outDirArg, outDirAbs, settleMs };
    const capture = async (page) => {
      let line;
      try {
        const result = await capturePage(tab, page, context);
        line = `${page.name} ${result.status} ${result.file}`;
        results.push({ name: page.name, ...result, ok: true, line });
      } catch (error) {
        const message = error?.message ?? String(error);
        log(`${page.name}: FAILED ${message}`);
        line = `${page.name} ERR -`;
        results.push({ name: page.name, status: "ERR", file: null, settled: false, ok: false, error: message, line });
      }
      lines.push(line);
      sinks.out(line);
    };

    for (const page of pages.filter((item) => !item.auth)) {
      await capture(page);
    }

    // Log in on the base origin, then reload so server-rendered auth state
    // (workspace data attributes) is picked up by the SPA.
    if (!/^https?:$/.test(new URL(await tab.url(), baseUrl).protocol)) {
      await tab.goto(toAbs("/", baseUrl));
    }
    await login(tab, { baseUrl, user: adminUser, password: adminPassword });
    await tab.reload();
    await tab.playwright.waitForLoadState({ state: "domcontentloaded" }).catch(() => {});

    for (const page of pages.filter((item) => item.auth)) {
      await capture(page);
    }
  } finally {
    // Never leave a browser tab behind.
    await tab.close().catch((error) => log(`closing tab failed: ${error?.message ?? error}`));
    restoreSinks();
    // Durable record of this run: same lines as stdout / onLine.
    await writeFile(join(outDirAbs, "shots.log"), `${lines.join("\n")}\n`).catch((error) =>
      log(`writing shots.log failed: ${error?.message ?? error}`),
    );
  }
  return results;
}

export async function main(argv = process.argv.slice(2)) {
  if (argv.includes("-h") || argv.includes("--help")) {
    usage(process.stdout);
    return 0;
  }
  const [baseUrl, outDir] = argv;
  const results = await captureAll({ baseUrl, outDir });
  const settled = results.filter((item) => item.settled).length;
  log(`captured ${results.length} pages (${settled} met their settle condition) into ${resolve(outDir)}`);
  return 0;
}

if (isCliRun) {
  main()
    .then((code) => process.exit(code))
    .catch((error) => {
      process.stderr.write(`shots: ERROR ${error?.stack ?? error}\n`);
      process.exit(1);
    });
}
