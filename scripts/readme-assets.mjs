import { createServer } from 'node:http'
import { readFile, writeFile, mkdir, stat, readdir } from 'node:fs/promises'
import { existsSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { join, dirname, extname, resolve, relative, isAbsolute } from 'node:path'
import { fileURLToPath } from 'node:url'

// Documentation only: real frontend + fictional fixtures, no provider or account access.
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const dist = join(root, 'internal/webui/dist')
const output = join(root, 'docs/images')
const profile = join(root, 'data/readme-capture/chrome-profile')
const port = 64098
const origin = `http://127.0.0.1:${port}`
const now = Date.parse('2026-10-16T10:00:00+08:00')
const iso = (days = 0, minutes = 0) => new Date(now + days * 86400000 + minutes * 60000).toISOString()
const sum = (xs, key) => xs.reduce((n, x) => n + (x[key] ?? 0), 0)
const wait = (ms) => new Promise((r) => setTimeout(r, ms))
const provider = { id: 'workbuddy', name: 'WorkBuddy', description: '国内版 / 国际版账号池', capabilities: ['quota', 'actions', 'tasks', 'settings'] }
const labels = ['日常编程', '长上下文', '工具调用', '项目重构', '轻量任务', '代码审查', '备用账号', '旅行补给']
const balances = [4820, 3650, 2940, 2360, 1870, 1420, 980, 760]
const statuses = ['active', 'active', 'active', 'active', 'active', 'active', 'cooling', 'paused']
const accounts = labels.map((label, i) => ({
  id: `sample${String(i + 1).padStart(2, '0')}`, provider: 'workbuddy', label, status: statuses[i],
  status_reason: i === 6 ? '限流冷却，稍后自动恢复' : i === 7 ? '手动暂停' : undefined,
  cooling_until: i === 6 ? iso(0, 12) : undefined,
  tags: [i === 7 ? '国际版' : '国内版'],
  quota: { unit: '积分', remaining: balances[i], total: 6000, updated_at: iso(), packages: [
    { name: '任务奖励积分', amount: 1500, remaining: 260 + i * 60, expire_at: iso(i * 2 + 2) },
    { name: '月度积分包', amount: 4500, remaining: balances[i] - (260 + i * 60), expire_at: iso(20 + i) },
  ] },
  stats: { success: 120 + i * 31, fail: i === 6 ? 2 : 0, in_flight: i < 2 ? 1 : 0, last_used_at: iso(0, -(i * 7 + 1)) },
}))
const forecasts = accounts.map((a, i) => {
  const pkgs = a.quota.packages.map((p, j) => ({ ...p, used: j === 0 && i < 2 ? p.remaining - 90 : p.remaining, waste: j === 0 && i < 2 ? 90 : 0 }))
  return { provider: a.provider, account_id: a.id, label: a.label, status: a.status,
    remaining: a.quota.remaining, total: a.quota.total, burn_per_day: 84 + i * 11,
    observed_days: 14, today_credit: 38 + i * 4, today_requests: 21 + i * 3,
    series: Array.from({ length: 14 }, (_, n) => 30 + ((n * 17 + i * 13) % 110)),
    urgency: Math.max(0.12, 0.88 - i * 0.1), note: i < 2 ? '优先使用即将到期积分' : '当前消耗节奏稳定',
    packages: pkgs, waste: sum(pkgs, 'waste'), required_per_day: 98 + i * 10,
    expiring_24h: 0, expiring_72h: i === 0 ? pkgs[0].remaining : 0,
    expiring_7d: i < 3 ? pkgs[0].remaining : 0, expiring_30d: a.quota.remaining,
    next_expire_at: pkgs[0].expire_at, next_expire_amount: pkgs[0].remaining, exhaust_at: iso(28 + i), no_expiry: 0,
  }
})
const calendar = Array.from({ length: 30 }, (_, i) => {
  const expiring = forecasts.flatMap((a) => a.packages).filter((p) => p.expire_at.slice(0, 10) === iso(i).slice(0, 10))
  return { date: iso(i).slice(0, 10), expiring: sum(expiring, 'remaining'), waste: sum(expiring, 'waste'), packages: expiring.length }
})
const credits = { updated_at: iso(), history_since: iso(-14), window_days: 14,
  strategy: { strategy: 'waste_first', waste_boost: 3, expensive_rate: 0.5, daily_cap: 0, reserve: 100, burn_window_days: 14 },
  pool: { remaining: sum(balances.map((remaining) => ({ remaining })), 'remaining'), burn_per_day: sum(forecasts, 'burn_per_day'),
    today_credit: sum(forecasts, 'today_credit'), expiring_24h: 0, expiring_72h: 260,
    expiring_7d: sum(forecasts, 'expiring_7d'), expiring_30d: sum(balances.map((remaining) => ({ remaining })), 'remaining'),
    waste: 180, ideal_waste: 0, required_per_day: sum(forecasts, 'required_per_day'),
    exhaust_at: iso(28), next_expire_at: iso(2), accounts: accounts.length },
  calendar, accounts: forecasts, tunable_providers: ['workbuddy'],
}
const summary = { id: 'workbuddy', name: 'WorkBuddy', total: 8, active: 6, cooling: 1, paused: 1, disabled: 0,
  remaining: credits.pool.remaining, quota: 48000, expiring_7d: credits.pool.expiring_7d }
const bucket = (key, requests, credit, failures = 0) => ({ key, requests, credit, failures,
  prompt_tokens: requests * 2400, completion_tokens: requests * 680, cached_tokens: requests * 1050, avg_ttfb_ms: 1260 })
const hourly = [4, 3, 2, 1, 1, 3, 8, 12, 18, 25, 32, 27, 22, 19, 28, 34, 30, 23, 17, 21, 28, 38, 31, 20]
const series = hourly.map((n, i) => bucket(iso(0, (i - 23) * 60), n, +(n * 0.872).toFixed(2), i === 11 || i === 18 ? 1 : 0))
const total = bucket('total', sum(series, 'requests'), +sum(series, 'credit').toFixed(2), sum(series, 'failures'))
const by = (names, weights) => {
  const rows = names.map((name, i) => bucket(name, Math.round(total.requests * weights[i]), +(total.credit * weights[i]).toFixed(2)))
  rows.at(-1).requests += total.requests - sum(rows, 'requests')
  rows.at(-1).credit = +(rows.at(-1).credit + total.credit - sum(rows, 'credit')).toFixed(2)
  rows[0].failures = total.failures
  return rows
}
const usage = { since: iso(-1), total, series,
  by_model: by(['glm-5.2', 'hy4-preview', 'glm-5.1', 'kimi-k3-1'], [0.48, 0.26, 0.17, 0.09]),
  by_account: by(accounts.slice(0, 4).map((a) => `workbuddy/${a.id}`), [0.4, 0.3, 0.18, 0.12]),
  by_key: by(['ttswitch', 'Claude Code', 'Codex CLI'], [0.5, 0.3, 0.2]),
  by_protocol: by(['chat', 'messages', 'responses'], [0.5, 0.3, 0.2]),
}
const models = [
  ['glm-5.2', 'GLM 5.2', '智谱', 200000, 48000, 0.05, false],
  ['glm-5.1', 'GLM 5.1', '智谱', 200000, 48000, 0.05, false],
  ['hy3', 'Hunyuan 3', '腾讯', 192000, 64000, 0, true],
  ['hy4-preview', 'Hunyuan 4 Preview', '腾讯', 1024000, 64000, 0.1, true],
  ['kimi-k3-1', 'Kimi K3.1', '月之暗面', 1048576, 262144, 0.2, true],
  ['minimax-m3', 'MiniMax M3', 'MiniMax', 512000, 196608, 0.1, false],
].map(([id, display_name, vendor, context_window, max_output_tokens, credit_rate, vision]) => ({
  id, provider: 'workbuddy', display_name, vendor, context_window, max_output_tokens, credit_rate,
  input_modalities: vision ? ['text', 'image'] : ['text'], supports_tools: true, supports_reasoning: true,
  reasoning_levels: ['low', 'medium', 'high'], reasoning_default_effort: 'high',
}))
const requests = Array.from({ length: 18 }, (_, i) => ({ id: `example-request-${i + 1}`, time: iso(0, -(i + 1)),
  protocol: ['chat', 'messages', 'responses'][i % 3], key_id: `example-key-${i % 3 + 1}`,
  key_name: ['ttswitch', 'Claude Code', 'Codex CLI'][i % 3], provider: 'workbuddy', account_id: accounts[i % 6].id,
  model: models[i % 4].id, requested_model: models[i % 4].id, stream: true, effort: 'high',
  status: 200, ok: true, attempts: 1, prompt_tokens: 2400 + i * 310, completion_tokens: 480 + i * 63,
  cached_tokens: 1100 + i * 90, reasoning_tokens: 240 + i * 20, credit: models[i % 4].credit_rate === 0 ? 0 : 2 + i % 7,
  has_credit: true, ttfb_ms: 1080 + i * 45, duration_ms: 4200 + i * 260,
}))
const keys = ['ttswitch', 'Claude Code', 'Codex CLI'].map((name, i) => ({ id: `example-key-${i + 1}`, name,
  secret: '<YOUR_API_KEY>', enabled: true, created_at: iso(-12), note: '展示用示例密钥，不可用于实际接入',
  rpm: [60, 30, 20][i], daily_credit_budget: [1000, 600, 300][i], credit_budget: 0,
  models: i === 2 ? ['glm-*'] : [], usage: { requests: 200 - i * 40, failures: 0, prompt_tokens: 480000 - i * 50000,
    completion_tokens: 136000, cached_tokens: 210000, credit: 173 - i * 33, day_credit: 83 - i * 21,
    day_requests: 95 - i * 20, last_used_at: iso(0, -(i + 1)) },
}))
const decisions = requests.slice(0, 8).map((r, i) => ({ time: r.time, provider: r.provider, model: r.model,
  rate: models[i % 4].credit_rate, chosen: accounts[i % 3].id, chosen_label: accounts[i % 3].label,
  reason: i % 3 ? '到期压力加权 · 优先使用近期到期积分' : '会话粘性命中 · 保持当前上下文连续',
  candidates: accounts.slice(0, 6).map((a, n) => ({ account_id: a.id, label: a.label, weight: 1 + n * 0.2,
    bias: n < 2 ? 2.6 : 1, share: [0.3, 0.24, 0.18, 0.12, 0.09, 0.07][n], tier: 0 })),
}))
const fixtures = {
  '/api/meta': { version: '1.0.2', started_at: iso(-2), providers: [provider], gateway: { model_naming: 'short', credit_in_model_name: false } },
  '/api/overview': { summary, providers: [summary], now: iso() },
  '/api/accounts': { accounts }, '/api/models': { models, errors: {} },
  '/api/credits': credits, '/api/credits/decisions': { decisions }, '/api/usage/stats': usage,
  '/api/requests': { requests }, '/api/keys': { keys },
  '/api/connect': { base_url: 'http://127.0.0.1:7863', openai: 'http://127.0.0.1:7863/v1', anthropic: 'http://127.0.0.1:7863', listen: '127.0.0.1:7863', has_legacy: false },
  '/api/settings/gateway': { model_naming: 'short', credit_in_model_name: false },
  '/api/providers/workbuddy': { meta: provider, actions: [
    { id: 'refresh', name: '刷新余额', bulk: true }, { id: 'checkin', name: '签到', bulk: true },
    { id: 'pause', name: '暂停', bulk: true }, { id: 'resume', name: '恢复', bulk: true },
  ], tasks: [], login_methods: [] },
  '/api/task-runs/latest': { latest: {} },
}
const mime = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8', '.svg': 'image/svg+xml', '.png': 'image/png', '.woff': 'font/woff', '.woff2': 'font/woff2' }
const initScript = `localStorage.setItem('poolsvc.theme', 'light'); localStorage.setItem('poolsvc.creditPrecision', 'int');
const RealDate = Date; const fixed = ${now}; window.Date = class extends RealDate { constructor(...a) { super(...(a.length ? a : [fixed])); } static now() { return fixed; } };`
let displayFont = ''
const cover = () => `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>PoolService · 产品封面</title><style>
@font-face{font-family:Display;src:url('/assets/${displayFont}')}*{box-sizing:border-box}body{margin:0;width:1600px;height:1380px;overflow:hidden;background:#f6f3ed;color:#211e18;font-family:'Microsoft YaHei',sans-serif}.stage{position:relative;width:100%;height:100%;background:radial-gradient(ellipse at 94% 72%,#e2c39170,transparent 55%)}.grid{position:absolute;inset:0;background-image:linear-gradient(#d8d0c240 1px,transparent 1px),linear-gradient(90deg,#d8d0c240 1px,transparent 1px);background-size:48px 48px;mask-image:linear-gradient(transparent 5%,#000 85%)}.top{position:absolute;left:60px;right:60px;top:46px;display:flex;justify-content:space-between;align-items:start}.eyebrow{font:12px Consolas,monospace;letter-spacing:3px;color:#9a7b4e}.brand{display:flex;align-items:center;gap:18px;margin:14px 0 0}.mark{width:64px;height:64px;border-radius:18px;background:#242019;position:relative}.mark:before,.mark:after{content:'';position:absolute;border:4px solid #efa33c;border-radius:50%;width:23px;height:23px;top:17px;left:10px}.mark:after{left:26px;border-color:#f6f3ed}.name{font:700 66px Display,'Microsoft YaHei',sans-serif;letter-spacing:-2px}.slogan{margin:16px 0 0;font-size:25px;font-weight:500;letter-spacing:1px}.right{text-align:right;padding-top:8px}.right h2{font-size:25px;line-height:1.6;margin:17px 0 12px;font-weight:500}.pills{display:flex;gap:8px;justify-content:flex-end}.pill{border:1px solid #dcd3c3;border-radius:999px;padding:7px 13px;font:12px Consolas,monospace;color:#7b6d57}.desktop{position:absolute;left:54px;top:250px;width:1390px;border:1px solid #3f382e;border-radius:15px;overflow:hidden;box-shadow:0 26px 60px #32271330;background:#1c1b18}.chrome{height:34px;display:flex;align-items:center;gap:6px;background:#28251f;border-bottom:1px solid #3d382e;padding:0 14px;color:#b9af9e;font:11px Consolas,monospace}.dot{width:7px;height:7px;border-radius:50%;background:#716654}.chrome span:last-child{margin-left:16px}.desktop img{display:block;width:100%}.phone{position:absolute;right:58px;top:785px;width:220px;padding:6px;border:1px solid #63533d;border-radius:25px;background:#242019;box-shadow:0 18px 55px #1c140f38;transform:rotate(3deg)}.phone img{display:block;width:100%;border-radius:19px}.footer{position:absolute;bottom:20px;left:60px;right:60px;display:flex;justify-content:space-between;font:12px Consolas,monospace;color:#9b8c77}.footer strong{color:#7a6241;font-weight:400}
</style><div class="stage"><div class="grid"></div><div class="top"><div><div class="eyebrow">YOUR ACCOUNTS. ONE GATEWAY.</div><div class="brand"><div class="mark"></div><div class="name">PoolService</div></div><p class="slogan">账号有序，积分可见，接入更简单。</p></div><div class="right"><div class="eyebrow">A CALMER WAY TO MANAGE AI</div><h2>把 AI 编程账号与请求，<br>放进一个清晰的管理面板。</h2><div class="pills"><span class="pill">OpenAI</span><span class="pill">Anthropic</span><span class="pill">Responses</span><span class="pill">Windows / Linux</span></div></div></div><div class="desktop"><div class="chrome"><i class="dot"></i><i class="dot"></i><i class="dot"></i><span>PoolService / Overview</span></div><img src="/images/overview-dark.png"></div><div class="phone"><img src="/images/mobile-accounts.png"></div><div class="footer"><strong>ACCOUNT POOL / CREDIT INSIGHTS / MULTI-PROTOCOL API</strong><span>真实界面 · 示例数据 · 桌面与移动端</span></div></div></html>`

async function startServer() {
  const server = createServer(async (req, res) => {
    try {
      const path = new URL(req.url, origin).pathname
      if (req.method !== 'GET') { res.writeHead(405); res.end('Read-only documentation preview'); return }
      if (path.startsWith('/api/')) {
        const value = fixtures[path]
        res.writeHead(value ? 200 : 404, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' })
        const data = path === '/api/requests' ? { requests: requests.slice(0, Number(new URL(req.url, origin).searchParams.get('limit') ?? 18)) } : value
        res.end(JSON.stringify(data ?? { error: 'Not available in documentation preview' })); return
      }
      if (path === '/_readme/cover') { res.writeHead(200, { 'Content-Type': mime['.html'] }); res.end(cover()); return }
      let file
      if (path.startsWith('/images/')) file = join(output, path.slice(8))
      else if (path.startsWith('/assets/') || path === '/favicon.svg') file = join(dist, path)
      else file = join(dist, 'index.html')
      const allowed = path.startsWith('/images/') ? output : dist
      const rel = relative(allowed, file)
      if (rel.startsWith('..') || isAbsolute(rel)) throw new Error('Invalid path')
      let bytes = await readFile(file)
      if (file.endsWith('index.html')) bytes = Buffer.from(bytes.toString().replace('<head>', `<head><script>${initScript}</script>`))
      res.writeHead(200, { 'Content-Type': mime[extname(file)] ?? 'application/octet-stream', 'Cache-Control': 'no-store' }); res.end(bytes)
    } catch { res.writeHead(404); res.end('Not found') }
  })
  await new Promise((ok, fail) => { server.once('error', fail); server.listen(port, '127.0.0.1', ok) })
  return server
}

class CDP {
  constructor(url) {
    this.socket = new WebSocket(url); this.id = 0; this.pending = new Map(); this.events = []
    this.ready = new Promise((ok, fail) => { this.socket.addEventListener('open', ok, { once: true }); this.socket.addEventListener('error', fail, { once: true }) })
    this.socket.addEventListener('message', ({ data }) => {
      const msg = JSON.parse(data)
      if (msg.id) { const p = this.pending.get(msg.id); if (!p) return; this.pending.delete(msg.id); clearTimeout(p.timer); msg.error ? p.reject(new Error(JSON.stringify(msg.error))) : p.resolve(msg.result) }
      else if (msg.method === 'Runtime.exceptionThrown' || msg.method === 'Log.entryAdded') this.events.push(msg)
    })
  }
  async send(method, params = {}, sessionId) {
    await this.ready
    return new Promise((resolve, reject) => {
      const id = ++this.id
      const timer = setTimeout(() => { this.pending.delete(id); reject(new Error(`CDP timeout: ${method}`)) }, 20000)
      this.pending.set(id, { resolve, reject, timer }); this.socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }))
    })
  }
  async evaluate(expression, sessionId) {
    const r = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }, sessionId)
    if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails))
    return r.result.value
  }
  close() { this.socket.close() }
}

async function launch() {
  const paths = [process.env.README_BROWSER, 'C:/Program Files/Google/Chrome/Application/chrome.exe', 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe', '/usr/bin/google-chrome', '/usr/bin/chromium'].filter(Boolean)
  const browser = paths.find(existsSync)
  if (!browser) throw new Error('Chrome or Edge not found; set README_BROWSER to its executable path')
  const child = spawn(browser, ['--headless=new', '--remote-debugging-port=0', '--remote-debugging-address=127.0.0.1', `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--disable-extensions', 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] })
  const url = await new Promise((ok, fail) => {
    let text = ''; const timer = setTimeout(() => fail(new Error('Browser startup timed out')), 20000)
    child.stderr.on('data', (chunk) => { text += chunk; const m = text.match(/DevTools listening on (ws:\/\/[^\s]+)/); if (m) { clearTimeout(timer); ok(m[1]) } })
    child.once('error', (e) => { clearTimeout(timer); fail(e) })
    child.once('exit', (code) => { clearTimeout(timer); fail(new Error(`Browser exited ${code}`)) })
  })
  return { child, cdp: new CDP(url) }
}

async function capture(cdp, session, name, path, width, height, dark = false, action) {
  await cdp.send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1.5, mobile: false }, session)
  await cdp.send('Page.navigate', { url: `${origin}${path}` }, session)
  for (let i = 0; i < 80; i++) {
    const ready = await cdp.evaluate(`document.readyState === 'complete' && ${path.startsWith('/_readme/') ? 'document.images.length > 0 && Array.from(document.images).every(i => i.complete && i.naturalWidth > 0)' : "!!document.querySelector('aside nav') && !document.querySelector('.animate-pulse')"}`, session)
    if (ready) break
    if (i === 79) throw new Error(`Page did not become ready: ${path}`)
    await wait(100)
  }
  await cdp.evaluate(`document.fonts.ready.then(() => { localStorage.setItem('poolsvc.theme', '${dark ? 'dark' : 'light'}'); document.documentElement.classList.toggle('dark', ${dark}); const s = document.createElement('style'); s.textContent = '*{animation:none!important;transition:none!important;caret-color:transparent!important}'; document.head.appendChild(s); })`, session)
  if (action) await cdp.evaluate(action, session)
  await wait(350)
  const geometry = await cdp.evaluate(`({ horizontal: document.documentElement.scrollWidth > innerWidth + 1, extra: document.querySelector('main > div') ? (() => { const el = document.querySelector('main > div'); return Math.max(0, el.scrollHeight - el.clientHeight) })() : 0 })`, session)
  if (geometry.horizontal) throw new Error(`Horizontal overflow: ${name}`)
  if (geometry.extra > 1 && width >= 1024) {
    height += geometry.extra + 2
    await cdp.send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1.5, mobile: false }, session)
    await wait(100)
  }
  const text = await cdp.evaluate('document.body.innerText', session)
  if (/输入管理密钥|Application Error|Unexpected Application Error/.test(text)) throw new Error(`Invalid screenshot page: ${name}`)
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false }, session)
  await writeFile(join(output, `${name}.png`), Buffer.from(data, 'base64'))
  console.log(`Captured ${name}: ${width}x${height}`)
}

async function main() {
  if (!existsSync(join(dist, 'index.html'))) throw new Error('Build the frontend first: npm --prefix web run build')
  displayFont = (await readdir(join(dist, 'assets'))).find((name) => name.startsWith('bricolage-grotesque-latin-wght-') && name.endsWith('.woff2'))
  if (!displayFont) throw new Error('Built display font not found')
  await mkdir(output, { recursive: true }); await mkdir(profile, { recursive: true })
  const server = await startServer()
  if (process.argv.includes('--serve')) { console.log(`Read-only example preview: ${origin}`); return }
  let browser
  try {
    browser = await launch(); const cdp = browser.cdp
    const { targetId } = await cdp.send('Target.createTarget', { url: 'about:blank' })
    const { sessionId } = await cdp.send('Target.attachToTarget', { targetId, flatten: true })
    await cdp.send('Page.enable', {}, sessionId); await cdp.send('Runtime.enable', {}, sessionId); await cdp.send('Log.enable', {}, sessionId)
    await capture(cdp, sessionId, 'overview-light', '/', 1440, 960)
    await capture(cdp, sessionId, 'overview-dark', '/', 1440, 960, true)
    await capture(cdp, sessionId, 'accounts', '/accounts', 1440, 820, false,
      `Array.from(document.querySelectorAll('input[aria-label="选择"]')).slice(0,2).forEach(el => el.click())`)
    await capture(cdp, sessionId, 'credits', '/credits', 1440, 1770)
    await capture(cdp, sessionId, 'gateway', '/gateway', 1440, 1100)
    await capture(cdp, sessionId, 'usage', '/usage', 1440, 1220)
    await capture(cdp, sessionId, 'models', '/models', 1440, 760)
    await capture(cdp, sessionId, 'mobile-accounts', '/accounts', 390, 844)
    await capture(cdp, sessionId, 'cover', '/_readme/cover', 1600, 1380)
    const errors = cdp.events.filter((e) => e.method === 'Runtime.exceptionThrown' || e.params.entry?.level === 'error')
    if (errors.length) throw new Error(`Browser errors: ${JSON.stringify(errors)}`)
    console.log('Verified: all pages rendered without browser errors; fictional fixtures only.')
    for (const name of ['cover', 'overview-light', 'overview-dark', 'accounts', 'credits', 'gateway', 'usage', 'models', 'mobile-accounts']) {
      const info = await stat(join(output, `${name}.png`)); console.log(`${name}.png ${(info.size / 1024).toFixed(0)} KiB`)
    }
  } finally {
    if (browser) { try { await browser.cdp.send('Browser.close') } catch {} browser.cdp.close(); browser.child.kill() }
    await new Promise((r) => server.close(r))
  }
}
main().catch((e) => { console.error(e.message); process.exitCode = 1 })
