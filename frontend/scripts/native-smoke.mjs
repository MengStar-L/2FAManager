// Run after building the current versioned EXE or build/bin/2FAManager.exe:
//   node frontend/scripts/native-smoke.mjs
// This exercises the real Wails bridge and WebView2 in an isolated data folder.
// go-webview2 deliberately removes debugger environment overrides. A separate
// QA executable uses a temporary dependency copy to enable loopback CDP/profile
// options. Application source, production executable and module cache stay intact.
// It deliberately never reads or writes the system clipboard.
import { chromium } from '@playwright/test';
import { createHash, createHmac } from 'node:crypto';
import { execFile, spawn } from 'node:child_process';
import { chmod, cp, mkdir, mkdtemp, readFile, stat, writeFile } from 'node:fs/promises';
import net from 'node:net';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const root = fileURLToPath(new URL('../../', import.meta.url));
const output = path.join(root, 'output');
const buildVersion = JSON.parse(await readFile(path.join(root, 'wails.json'), 'utf8')).info.productVersion;
const executableCandidates = process.env.LUMA_NATIVE_EXE ? [path.resolve(process.env.LUMA_NATIVE_EXE)] : [
  path.join(root, 'build', 'bin', `2FAManager-${buildVersion}.exe`), path.join(root, 'build', 'bin', '2FAManager.exe'),
];
let executable;
const fixturePNG = path.join(root, 'frontend', 'tests', 'fixtures', 'totp-test.png');
const screenshot = path.join(output, 'native-populated.png');
const checks = [];
const outlines = [];
const secrets = new Set();
const sessions = new Set();
let activeSession;
let isolatedRoot;
let report;
let launchedExecutable;
let productionHash;
const execute = promisify(execFile);

const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
function check(condition, message) {
  if (!condition) throw new Error(message);
}
function passed(name) {
  checks.push(name);
  console.log(`PASS ${name}`);
}
function safeMessage(error) {
  let message = error instanceof Error ? error.message : String(error);
  for (const secret of secrets) {
    if (secret) message = message.split(secret).join('[redacted fixture data]');
  }
  return message.replace(/otpauth:\/\/\S+/gi, '[redacted fixture URI]');
}

async function freeLoopbackPort() {
  const server = net.createServer();
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const { port } = server.address();
  await new Promise((resolve, reject) => server.close(err => err ? reject(err) : resolve()));
  return port;
}

async function installWindowProbe() {
  // Inspect only the exact child PID. GetWindowPlacement gives physical-pixel
  // restore bounds even while minimised/maximised; no other apps are touched.
  await writeFile(path.join(isolatedRoot, 'window-state.ps1'), `param([int]$ProcessId,[switch]$TrayClick)
Add-Type -TypeDefinition @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public static class LumaWindowProbe {
  public delegate bool EnumProc(IntPtr hwnd, IntPtr param);
  [StructLayout(LayoutKind.Sequential)] public struct POINT { public int X, Y; }
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [StructLayout(LayoutKind.Sequential)] public struct PLACEMENT { public int Length, Flags, Show; public POINT Min, Max; public RECT Normal; }
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc proc, IntPtr param);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hwnd, out uint pid);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr hwnd, StringBuilder text, int count);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hwnd);
  [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr hwnd);
  [DllImport("user32.dll")] public static extern bool IsZoomed(IntPtr hwnd);
  [DllImport("user32.dll")] public static extern bool GetWindowPlacement(IntPtr hwnd, ref PLACEMENT placement);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hwnd, out RECT rect);
  [DllImport("user32.dll")] public static extern int GetWindowRgn(IntPtr hwnd, IntPtr region);
  [DllImport("user32.dll")] public static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
  [DllImport("gdi32.dll")] public static extern IntPtr CreateRectRgn(int left, int top, int right, int bottom);
  [DllImport("gdi32.dll")] public static extern bool PtInRegion(IntPtr region, int x, int y);
  [DllImport("gdi32.dll")] public static extern bool DeleteObject(IntPtr obj);
  [DllImport("dwmapi.dll")] public static extern int DwmGetWindowAttribute(IntPtr hwnd, uint attr, out uint value, uint size);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr FindWindow(string className, string title);
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hwnd, uint message, IntPtr wParam, IntPtr lParam);
  public static IntPtr FindTray(uint expected) { return FindWindow("LumaTrayWindow-"+expected,null); }
  public static IntPtr Find(uint expected) {
    var ownWindow=FindWindow("LumaAuthenticatorWindow-"+expected,null);
    uint ownPid; GetWindowThreadProcessId(ownWindow,out ownPid);
    if(ownWindow!=IntPtr.Zero && ownPid==expected) return ownWindow;
    IntPtr found=IntPtr.Zero;
    EnumWindows(delegate(IntPtr hwnd, IntPtr _) {
      uint owner; GetWindowThreadProcessId(hwnd, out owner); if(owner!=expected) return true;
      var title=new StringBuilder(512); GetWindowText(hwnd,title,title.Capacity);
      var p=new PLACEMENT(); p.Length=Marshal.SizeOf(p); GetWindowPlacement(hwnd,ref p);
      if(title.ToString().StartsWith("Luma") && p.Normal.Right-p.Normal.Left>300) { found=hwnd; return false; }
      return true;
    },IntPtr.Zero); return found;
  }
}
'@
$null=[LumaWindowProbe]::SetThreadDpiAwarenessContext([IntPtr](-4))
if($TrayClick) {
  $tray=[LumaWindowProbe]::FindTray([uint32]$ProcessId)
  [uint32]$owner=0
  $null=[LumaWindowProbe]::GetWindowThreadProcessId($tray,[ref]$owner)
  if($tray -eq [IntPtr]::Zero -or $owner -ne $ProcessId) { throw 'Isolated test tray window was not found' }
  @{ sent=[LumaWindowProbe]::PostMessage($tray,0x8001,[IntPtr]::Zero,[IntPtr]0x202) } | ConvertTo-Json -Compress
  exit
}
$handle=[LumaWindowProbe]::Find([uint32]$ProcessId)
if($handle -eq [IntPtr]::Zero) { @{ found=$false } | ConvertTo-Json -Compress; exit }
$placement=New-Object LumaWindowProbe+PLACEMENT
$placement.Length=[Runtime.InteropServices.Marshal]::SizeOf($placement)
$null=[LumaWindowProbe]::GetWindowPlacement($handle,[ref]$placement)
$bounds=New-Object LumaWindowProbe+RECT
$null=[LumaWindowProbe]::GetWindowRect($handle,[ref]$bounds)
$width=$bounds.Right-$bounds.Left
$height=$bounds.Bottom-$bounds.Top
$region=[LumaWindowProbe]::CreateRectRgn(0,0,0,0)
try {
  $regionType=[LumaWindowProbe]::GetWindowRgn($handle,$region)
  $corners=@([LumaWindowProbe]::PtInRegion($region,0,0),[LumaWindowProbe]::PtInRegion($region,$width-1,0),[LumaWindowProbe]::PtInRegion($region,0,$height-1),[LumaWindowProbe]::PtInRegion($region,$width-1,$height-1))
  $edgeMidpoints=@([LumaWindowProbe]::PtInRegion($region,[int]($width/2),0),[LumaWindowProbe]::PtInRegion($region,[int]($width/2),$height-1),[LumaWindowProbe]::PtInRegion($region,0,[int]($height/2)),[LumaWindowProbe]::PtInRegion($region,$width-1,[int]($height/2)))
  $center=[LumaWindowProbe]::PtInRegion($region,[int]($width/2),[int]($height/2))
} finally { $null=[LumaWindowProbe]::DeleteObject($region) }
[uint32]$cornerPreference=0
$dwmResult=[LumaWindowProbe]::DwmGetWindowAttribute($handle,33,[ref]$cornerPreference,4)
@{ found=$true; visible=[LumaWindowProbe]::IsWindowVisible($handle); minimized=[LumaWindowProbe]::IsIconic($handle); maximized=[LumaWindowProbe]::IsZoomed($handle); normal=@{ x=$placement.Normal.Left; y=$placement.Normal.Top; width=$placement.Normal.Right-$placement.Normal.Left; height=$placement.Normal.Bottom-$placement.Normal.Top }; shape=@{ width=$width; height=$height; regionType=$regionType; corners=$corners; edgeMidpoints=$edgeMidpoints; center=$center; dwmResult=$dwmResult; cornerPreference=$cornerPreference } } | ConvertTo-Json -Depth 4 -Compress
`);
}

async function windowState(session) {
  const { stdout } = await execute('powershell.exe', ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', path.join(isolatedRoot, 'window-state.ps1'), '-ProcessId', String(session.child.pid)], {
    windowsHide: true, timeout: 10000, maxBuffer: 1 << 20,
  });
  return JSON.parse(stdout.trim());
}

async function waitWindow(session, predicate, description) {
  const deadline = Date.now() + 10000;
  let latest;
  do {
    const state = await windowState(session);
    latest = state;
    if (predicate(state)) return state;
    await delay(100);
  } while (Date.now() < deadline);
  throw new Error(`Native window did not ${description}: ${JSON.stringify(latest)}`);
}

function sameBounds(actual, expected) {
  return ['x', 'y', 'width', 'height'].every(key => Math.abs(actual[key] - expected[key]) <= 2);
}

function hasRoundedOutline(state) {
  const shape = state.shape;
  return shape?.regionType === 3 && shape.corners.length === 4 && shape.corners.every(inside => inside === false) && shape.edgeMidpoints.length === 4 && shape.edgeMidpoints.every(inside => inside === true) && shape.center === true;
}

function recordOutline(name, state) { outlines.push({ name, maximized: state.maximized, shape: state.shape }); }

async function buildQAExecutable() {
  const options = { cwd: root, windowsHide: true, timeout: 60000, maxBuffer: 4 << 20 };
  const { stdout } = await execute('go', ['list', '-m', '-f', '{{.Dir}}', 'github.com/wailsapp/go-webview2'], options);
  const dependencyDirectory = stdout.trim();
  const dependencyFile = path.join(dependencyDirectory, 'pkg', 'edge', 'chromium.go');
  const source = await readFile(dependencyFile, 'utf8');
  const original = 'browserArgs := strings.Join(e.AdditionalBrowserArgs, " ")';
  check(source.split(original).length === 2, 'WebView2 source changed; review the test-only dependency patch before running native QA');
  const modified = source.replace(original, `${original}\n\tif qa := os.Getenv("LUMA_QA_BROWSER_ARGUMENTS"); qa != "" { browserArgs += " " + qa }\n\tif qa := os.Getenv("LUMA_QA_WEBVIEW_PROFILE"); qa != "" { dataPath = qa }`);
  // Recent Go releases forbid overlays targeting GOMODCACHE. Copy this one
  // dependency and select it through a temporary modfile, never edit the cache.
  const dependencyCopy = path.join(isolatedRoot, 'go-webview2-qa');
  await cp(dependencyDirectory, dependencyCopy, { recursive: true });
  const copiedSource = path.join(dependencyCopy, 'pkg', 'edge', 'chromium.go');
  await chmod(copiedSource, 0o600); // Go's module-cache files are read-only; only this copied file changes.
  await writeFile(copiedSource, modified);
  const modfile = path.join(isolatedRoot, 'qa.mod');
  const originalModfile = await readFile(path.join(root, 'go.mod'), 'utf8');
  const originalSumfile = await readFile(path.join(root, 'go.sum'), 'utf8');
  await writeFile(modfile, `${originalModfile}\nreplace github.com/wailsapp/go-webview2 => ${JSON.stringify(dependencyCopy.replaceAll('\\', '/'))}\n`);
  await writeFile(path.join(isolatedRoot, 'qa.sum'), originalSumfile);
  launchedExecutable = path.join(isolatedRoot, 'qa-2FAManager.exe');
  await execute('go', ['build', '-modfile', modfile, '-tags', 'desktop,wv2runtime.download,production', '-ldflags', '-w -s -H windowsgui', '-o', launchedExecutable, '.'], {
    ...options, env: { ...process.env, CGO_ENABLED: '0', GOOS: 'windows', GOARCH: 'amd64' },
  });
  check(await readFile(dependencyFile, 'utf8') === source, 'QA build unexpectedly changed the module cache');
  check(await readFile(path.join(root, 'go.mod'), 'utf8') === originalModfile && await readFile(path.join(root, 'go.sum'), 'utf8') === originalSumfile, 'QA build unexpectedly changed application dependency files');
  passed('separate native QA executable with test-only CDP hook; production code unchanged');
}

async function makeVideoFixture(destination) {
  const browser = await chromium.launch({ channel: 'msedge', headless: true });
  try {
    const page = await browser.newPage();
    const encoded = await page.evaluate(async () => {
      const mimeType = ['video/webm;codecs=vp9', 'video/webm;codecs=vp8', 'video/webm']
        .find(type => MediaRecorder.isTypeSupported(type));
      if (!mimeType) throw new Error('This browser cannot record a synthetic WebM fixture');
      const canvas = document.createElement('canvas');
      canvas.width = 640;
      canvas.height = 360;
      const context = canvas.getContext('2d');
      const draw = frame => {
        const gradient = context.createLinearGradient(0, 0, 640, 360);
        gradient.addColorStop(0, '#f2edff');
        gradient.addColorStop(1, '#e2f7ee');
        context.fillStyle = gradient;
        context.fillRect(0, 0, 640, 360);
        context.fillStyle = '#b4a2df';
        context.beginPath();
        context.arc(180 + Math.sin(frame / 6) * 80, 180, 75, 0, Math.PI * 2);
        context.fill();
      };
      draw(0);
      const stream = canvas.captureStream(24);
      const recorder = new MediaRecorder(stream, { mimeType, videoBitsPerSecond: 400000 });
      const chunks = [];
      recorder.ondataavailable = event => { if (event.data.size) chunks.push(event.data); };
      const finished = new Promise((resolve, reject) => {
        recorder.onstop = resolve;
        recorder.onerror = () => reject(new Error('Synthetic video recording failed'));
      });
      recorder.start();
      try {
        for (let frame = 1; frame <= 36; frame++) {
          draw(frame);
          await new Promise(resolve => setTimeout(resolve, 42));
        }
        recorder.stop();
        await finished;
        const bytes = new Uint8Array(await new Blob(chunks, { type: mimeType }).arrayBuffer());
        let binary = '';
        for (const byte of bytes) binary += String.fromCharCode(byte);
        return btoa(binary);
      } finally {
        for (const track of stream.getTracks()) track.stop();
      }
    });
    const bytes = Buffer.from(encoded, 'base64');
    check(bytes.length > 100 && bytes.subarray(0, 4).equals(Buffer.from([0x1a, 0x45, 0xdf, 0xa3])), 'Synthetic WebM fixture is invalid');
    await writeFile(destination, bytes);
    return bytes;
  } finally {
    await browser.close();
  }
}

async function launchNative(appData, localData) {
  const port = await freeLoopbackPort();
  const env = {
    ...process.env,
    APPDATA: appData,
    LOCALAPPDATA: localData,
    // Give each launch a separate renderer profile so a briefly lingering
    // WebView2 process cannot reuse the previous debugging port. APPDATA,
    // and therefore the encrypted vault and settings, stays the same.
    LUMA_QA_WEBVIEW_PROFILE: path.join(isolatedRoot, `webview-profile-${port}`),
    LUMA_QA_BROWSER_ARGUMENTS: `--remote-debugging-port=${port} --remote-debugging-address=127.0.0.1`,
  };
  const child = spawn(launchedExecutable, [], {
    cwd: path.dirname(launchedExecutable),
    windowsHide: true,
    stdio: 'ignore',
    env,
  });
  const session = { child, env, browser: null, page: null, exited: false, spawnError: null, closed: null };
  session.closed = new Promise(resolve => {
    child.once('exit', () => { session.exited = true; resolve(); });
    child.once('error', error => { session.spawnError = error; session.exited = true; resolve(); });
  });
  sessions.add(session);
  activeSession = session;
  const endpoint = `http://127.0.0.1:${port}`;
  const deadline = Date.now() + 30000;
  let debuggerReady = false;
  while (Date.now() < deadline) {
    if (session.spawnError) throw session.spawnError;
    check(!session.exited, 'Native test process exited before WebView2 was ready. Close any separately running Luma instance before testing.');
    try {
      const response = await fetch(`${endpoint}/json/version`, { signal: AbortSignal.timeout(1000) });
      if (response.ok && (await response.json()).webSocketDebuggerUrl) {
        debuggerReady = true;
        break;
      }
    } catch { /* WebView2 is still starting. */ }
    await delay(200);
  }
  check(debuggerReady, 'WebView2 did not expose its isolated loopback debugging endpoint');
  session.browser = await chromium.connectOverCDP(endpoint, { timeout: 10000 });
  while (Date.now() < deadline) {
    for (const context of session.browser.contexts()) {
      for (const page of context.pages()) {
        if (await page.evaluate(() => Boolean(window.go?.main?.App && window.runtime?.Quit)).catch(() => false)) {
          session.page = page;
          page.setDefaultTimeout(10000);
          page.setDefaultNavigationTimeout(15000);
          // windowsHide suppresses a stray launcher console and can also mark
          // the GUI's first ShowWindow hidden. Show this isolated QA window
          // explicitly before checking its real minimise/tray lifecycle.
          await page.evaluate(() => window.runtime.WindowShow());
          return session;
        }
      }
    }
    await delay(100);
  }
  throw new Error('Native Wails bridge did not become available');
}

async function restoreViaSecondInstance(session) {
  const child = spawn(launchedExecutable, [], { cwd: path.dirname(launchedExecutable), windowsHide: true, stdio: 'ignore', env: session.env });
  const second = { child, browser: null, page: null, exited: false, spawnError: null, closed: null };
  second.closed = new Promise(resolve => {
    child.once('exit', () => { second.exited = true; resolve(); });
    child.once('error', error => { second.spawnError = error; second.exited = true; resolve(); });
  });
  sessions.add(second);
  await Promise.race([second.closed, delay(5000)]);
  check(!second.spawnError && second.exited, 'Opening a second isolated instance did not hand off to the original process');
  sessions.delete(second);
  await waitWindow(session, state => state.found && state.visible && !state.minimized, 'restore when reopened');
}

async function restoreViaTray(session) {
  const { stdout } = await execute('powershell.exe', ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', path.join(isolatedRoot, 'window-state.ps1'), '-ProcessId', String(session.child.pid), '-TrayClick'], {
    windowsHide: true, timeout: 10000, maxBuffer: 1 << 20,
  });
  check(JSON.parse(stdout.trim()).sent, 'Synthetic left-click was not delivered to the isolated native tray callback');
  await waitWindow(session, state => state.found && state.visible && !state.minimized, 'restore through its tray callback');
}

async function expectAppExit(session, method) {
  check(await session.page.evaluate(method => typeof window.go?.main?.App?.[method] === 'function', method), `Missing native bridge method: ${method}`);
  // The window can vanish before Wails sends the method result back over CDP.
  await bridge(session.page, method).catch(error => {
    if (!/closed|destroyed|Target|context/i.test(String(error))) throw error;
  });
  await Promise.race([session.closed, delay(7000)]);
  check(session.exited, `Native ${method} did not exit its process`);
}

async function bridge(page, method, ...args) {
  return page.evaluate(({ method, args }) => {
    const api = window.go?.main?.App;
    if (typeof api?.[method] !== 'function') throw new Error(`Missing native bridge method: ${method}`);
    return api[method](...args);
  }, { method, args });
}

async function assertIsolatedState(page, expectedDirectory, count) {
  const state = await bridge(page, 'GetState');
  check(path.resolve(state.dataPath).toLowerCase() === path.resolve(expectedDirectory).toLowerCase(), 'Refusing to mutate a vault outside the isolated test data directory');
  check(Array.isArray(state.tokens) && state.tokens.length === count, 'Unexpected token count in the isolated native vault');
  return state;
}

async function expectRejected(page, method, ...args) {
  let rejected = false;
  try { await bridge(page, method, ...args); } catch { rejected = true; }
  check(rejected, `Native ${method} accepted input that must be rejected`);
}

function decodeBase32(value) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let accumulator = 0;
  let available = 0;
  const result = [];
  for (const letter of value.toUpperCase().replace(/[=\s]/g, '')) {
    const digit = alphabet.indexOf(letter);
    check(digit >= 0, 'Synthetic fixture contains an invalid Base32 key');
    accumulator = (accumulator << 5) | digit;
    available += 5;
    if (available >= 8) {
      available -= 8;
      result.push((accumulator >>> available) & 255);
      accumulator &= (1 << available) - 1;
    }
  }
  return Buffer.from(result);
}

function codeAt(secret, token, unixMilliseconds) {
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(unixMilliseconds / 1000 / token.period)));
  const digest = createHmac(token.algorithm.toLowerCase(), decodeBase32(secret)).update(counter).digest();
  const offset = digest[digest.length - 1] & 15;
  const number = digest.readUInt32BE(offset) & 0x7fffffff;
  return String(number % (10 ** token.digits)).padStart(token.digits, '0');
}

async function assertNativeCode(page, secret, id) {
  const before = Date.now();
  const tokens = await bridge(page, 'GetTokens');
  const after = Date.now();
  const token = tokens.find(candidate => candidate.id === id);
  check(token && /^\d{6,8}$/.test(token.code), 'Native vault did not generate a numeric TOTP code');
  check(token.code === codeAt(secret, token, before) || token.code === codeAt(secret, token, after), 'Native TOTP does not match the independent Node HMAC calculation');
  check(token.remaining > 0 && token.remaining <= token.period, 'Native TOTP countdown is outside its period');
  check(!Object.prototype.hasOwnProperty.call(token, 'secret') && !JSON.stringify(token).includes(secret), 'Native token metadata unexpectedly exposes its key');
  return token;
}

async function assertVideoAndRange(page, settings, expectedBytes) {
  await page.waitForFunction(url => {
    const video = document.querySelector('video');
    return video && video.getAttribute('src') === url && video.readyState >= 2 && video.currentTime > 0 && !video.paused && !video.error;
  }, settings.backgroundUrl, { timeout: 15000 });
  const range = await page.evaluate(async url => {
    const response = await fetch(url, { headers: { Range: 'bytes=0-15' } });
    return { status: response.status, range: response.headers.get('content-range'), bytes: [...new Uint8Array(await response.arrayBuffer())] };
  }, settings.backgroundUrl);
  check(range.status === 206 && range.range?.startsWith('bytes 0-15/') && Buffer.from(range.bytes).equals(expectedBytes.subarray(0, 16)), 'Native Wails background handler did not return the correct video byte range');
}

async function closeNative(session) {
  if (!session) return;
  if (!session.exited && session.page && !session.page.isClosed()) {
    await session.page.evaluate(() => window.go?.main?.App?.QuitApp ? window.go.main.App.QuitApp() : window.runtime?.Quit()).catch(() => {});
    await Promise.race([session.closed, delay(5000)]);
  }
  if (!session.exited && session.child.pid) {
    // Only terminate the exact child launched by this script and its WebView2
    // descendants. Never match or stop processes by application name.
    await new Promise(resolve => {
      const killer = spawn('taskkill.exe', ['/PID', String(session.child.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' });
      killer.once('error', resolve);
      killer.once('exit', resolve);
    });
    await Promise.race([session.closed, delay(3000)]);
  }
  if (session.browser) await session.browser.close().catch(() => {});
  sessions.delete(session);
  if (activeSession === session) activeSession = null;
  check(session.exited, 'Native test child did not exit during cleanup');
}

try {
  check(process.platform === 'win32', 'Native smoke testing requires Windows');
  for (const candidate of executableCandidates) {
    if (await stat(candidate).then(entry => entry.isFile()).catch(() => false)) { executable = candidate; break; }
  }
  check(executable, 'Build the Windows application before running native smoke tests');
  check((await stat(executable)).isFile(), 'Build the Windows application before running native smoke tests');
  productionHash = createHash('sha256').update(await readFile(executable)).digest('hex');
  const png = await readFile(fixturePNG);
  await mkdir(path.join(output, 'native-qa'), { recursive: true });
  isolatedRoot = await mkdtemp(path.join(output, 'native-qa', 'run-'));
  await installWindowProbe();
  await buildQAExecutable();
  const appData = path.join(isolatedRoot, 'roaming');
  const localData = path.join(isolatedRoot, 'local');
  const dataDirectory = path.join(appData, 'LumaAuthenticator');
  await mkdir(dataDirectory, { recursive: true });
  await mkdir(localData, { recursive: true });
  const videoName = 'bg-native-smoke.webm';
  const video = await makeVideoFixture(path.join(dataDirectory, videoName));
  const settings = { backgroundType: 'video', pattern: 'dots', backgroundUrl: `/media/${videoName}`, backgroundName: 'Native smoke synthetic video', opacity: 0.18, motion: true };
  await writeFile(path.join(dataDirectory, 'settings.json'), JSON.stringify(settings));
  passed('synthetic video fixture in fresh isolated APPDATA');

  let session = await launchNative(appData, localData);
  let page = session.page;
  const initial = await assertIsolatedState(page, dataDirectory, 0);
  check(initial.settings.checkUpdatesAutomatically === true && initial.settings.updateAutomatically === false, 'Legacy settings unexpectedly enable unattended installation');
  const updateState = await bridge(page, 'GetUpdateStatus');
  check(updateState.currentVersion === buildVersion && updateState.releaseUrl === 'https://github.com/MengStar-L/2FAManager/releases/latest', 'Native update bridge has the wrong version or release source');
  await expectRejected(page, 'DownloadUpdate');
  await expectRejected(page, 'InstallUpdate');
  passed('native updater reports the packaged version and refuses download/install without a checked candidate');
  check(initial.settings.closeToTray === true && initial.settings.theme === 'regular' && initial.settings.accentColor === '#8773b7', 'Legacy settings did not default to regular theme, default accent and close-to-tray behavior');
  check(initial.session?.filter === 'all' && initial.session?.search === '' && initial.session?.sidebarCollapsed === false, 'Fresh native session did not start with empty default navigation');
  settings.theme = initial.settings.theme;
  settings.closeToTray = initial.settings.closeToTray;
  settings.accentColor = initial.settings.accentColor;
  settings.checkUpdatesAutomatically = false;
  settings.updateAutomatically = false;
  const initialOutline = await waitWindow(session, state => state.found && state.visible && hasRoundedOutline(state), 'expose a truly rounded native window region');
  recordOutline('initial normal', initialOutline);
  passed('real native window region excludes four corners and retains edge midpoints/center');
  passed('real Wails bridge and initially empty isolated vault');
  await assertVideoAndRange(page, settings, video);
  passed('native WebView2 video playback and HTTP 206 byte ranges');

  const previews = await bridge(page, 'PreviewImage', `data:image/png;base64,${png.toString('base64')}`);
  check(previews.length === 1 && previews[0].uri.startsWith('otpauth://'), 'Synthetic QR fixture did not produce exactly one TOTP preview');
  const uri = previews[0].uri;
  const secret = new URL(uri).searchParams.get('secret');
  check(secret, 'Synthetic QR preview is missing its test key');
  secrets.add(secret);
  secrets.add(uri);
  await bridge(page, 'ImportTokens', [uri]);
  const state = await assertIsolatedState(page, dataDirectory, 1);
  const id = state.tokens[0].id;
  let token = await assertNativeCode(page, secret, id);
  passed('native QR image import and independently verified TOTP');

  await bridge(page, 'ToggleFavorite', id);
  await bridge(page, 'UpdateToken', id, {
    issuer: 'Native smoke · 示例', account: 'native-smoke@example.test', group: '本机验收', secret: '', color: '#8b7fd6',
    algorithm: token.algorithm, digits: token.digits, period: token.period,
  });
  token = await assertNativeCode(page, secret, id);
  check(token.favorite && token.group === '本机验收' && token.account === 'native-smoke@example.test', 'Native edit did not preserve favorite or update metadata');
  passed('native favorite, metadata/group editing and empty-key preservation');

  await expectRejected(page, 'ImportTokens', [uri]);
  await expectRejected(page, 'PreviewImage', 'data:image/png;base64,bm90LWFuLWltYWdl');
  await assertIsolatedState(page, dataDirectory, 1);
  passed('duplicate import and malformed image rejected without mutation');
  settings.opacity = 0.27;
  await bridge(page, 'SaveSettings', settings);
  settings.opacity = 0.23;
  await bridge(page, 'SaveSettings', settings);
  await expectRejected(page, 'SaveSettings', { ...settings, accentColor: 'not-a-color' });
  await expectRejected(page, 'SaveSettings', { ...settings, updateAutomatically: true, checkUpdatesAutomatically: false });
  check((await bridge(page, 'GetState')).settings.accentColor === settings.accentColor, 'Invalid accent save changed the native settings');
  passed('repeated native appearance saves replace the existing settings file');

  const encrypted = await readFile(path.join(dataDirectory, 'vault.dat'));
  check(encrypted.subarray(0, 10).toString() === 'LUMA-VAULT' && !encrypted.includes(Buffer.from(secret)) && !encrypted.includes(Buffer.from(token.account)), 'Native vault file has an unexpected format or exposes fixture secrets');
  passed('native DPAPI vault contains no plaintext fixture key or account');
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => Boolean(window.go?.main?.App));
  await page.getByText('native-smoke@example.test', { exact: true }).first().waitFor();
  await assertVideoAndRange(page, settings, video);
  await page.locator('.token-card, .add-card').evaluateAll(async elements => {
    await Promise.all(elements.flatMap(element => element.getAnimations().map(animation => animation.finished.catch(() => {}))));
  });
  await page.screenshot({ path: screenshot, fullPage: true });
  passed('actual native populated UI screenshot');

  // Save navigation through real controls so this covers the UI-to-Go bridge,
  // not only persistence methods. Every account and search here is synthetic.
  await page.getByRole('button', { name: '收起侧栏', exact: true }).click();
  await page.getByRole('navigation', { name: '令牌分组' }).getByRole('button', { name: '本机验收', exact: true }).click();
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  await page.getByRole('textbox', { name: '搜索令牌' }).fill('native-smoke');
  const expectedSession = { sidebarCollapsed: true, filter: 'group:本机验收', search: 'native-smoke' };
  await page.waitForFunction(async expected => {
    const current = (await window.go.main.App.GetState()).session;
    return Object.entries(expected).every(([key, value]) => current?.[key] === value);
  }, expectedSession);
  settings.theme = 'anime';
  settings.accentColor = '#167e92';
  settings.closeToTray = true;
  await bridge(page, 'SaveSettings', settings);
  await page.evaluate(() => { window.runtime.WindowSetSize(1010, 690); window.runtime.WindowSetPosition(123, 117); });
  await page.waitForFunction(async () => {
    const size = await window.runtime.WindowGetSize();
    const position = await window.runtime.WindowGetPosition();
    return size.w === 1010 && size.h === 690 && position.x === 123 && position.y === 117;
  });
  const normalWindow = await waitWindow(session, state => state.found && state.visible && !state.minimized && !state.maximized && hasRoundedOutline(state), 'retain a rounded native outline after resizing');
  recordOutline('resized normal', normalWindow);
  passed('resizing preserves the real rounded native window region');
  const expectedBounds = normalWindow.normal;
  check(expectedBounds.width >= 760 && expectedBounds.height >= 560, 'Native test window size is below the application minimum');
  await page.getByRole('button', { name: '最小化', exact: true }).click();
  const minimized = await waitWindow(session, state => state.found && state.minimized, 'minimise');
  check(sameBounds(minimized.normal, expectedBounds), 'Minimising overwrote the normal window rectangle');
  await restoreViaSecondInstance(session);
  passed('native minimise and second-instance restore preserve normal bounds');

  await page.getByRole('button', { name: '关闭窗口', exact: true }).click();
  await waitWindow(session, state => state.found && !state.visible, 'hide on close');
  check(!session.exited, 'Default close-to-tray terminated the application');
  await assertIsolatedState(page, dataDirectory, 1);
  await restoreViaTray(session);
  await page.getByText('native-smoke@example.test', { exact: true }).first().waitFor();
  passed('default close hides the live native process and its tray callback restores the same vault');

  await page.getByRole('button', { name: '最小化', exact: true }).click();
  await waitWindow(session, state => state.found && state.minimized, 'minimise before explicit quit');
  await expectAppExit(session, 'QuitApp');
  await closeNative(session);
  const savedNormal = JSON.parse(await readFile(path.join(dataDirectory, 'session.json'), 'utf8'));
  check(savedNormal.window?.valid && !savedNormal.window?.maximized && sameBounds(savedNormal.window, expectedBounds), 'Normal window size/position did not persist on explicit quit');
  passed('QuitApp exits despite close-to-tray and preserves normal geometry while minimised');
  session = await launchNative(appData, localData);
  page = session.page;
  const persisted = await assertIsolatedState(page, dataDirectory, 1);
  token = await assertNativeCode(page, secret, id);
  check(token.favorite && token.group === '本机验收' && token.account === 'native-smoke@example.test', 'Native token metadata did not survive process restart');
  check(persisted.settings.backgroundUrl === settings.backgroundUrl && persisted.settings.opacity === settings.opacity && persisted.settings.theme === 'anime' && persisted.settings.accentColor === settings.accentColor && persisted.settings.closeToTray === true, 'Native appearance preferences did not survive process restart');
  check(Object.entries(expectedSession).every(([key, value]) => persisted.session?.[key] === value), 'Native sidebar/filter/search session did not survive process restart');
  await page.locator('.app.theme-anime.sidebar-collapsed').waitFor();
  await page.waitForFunction(() => getComputedStyle(document.querySelector('.toolbar .primary-action')).backgroundColor === 'rgb(22, 126, 146)');
  check(await page.getByRole('textbox', { name: '搜索令牌' }).inputValue() === expectedSession.search, 'Native search field did not restore its saved query');
  await page.locator('.token-card').waitFor();
  await waitWindow(session, state => state.found && state.visible && !state.minimized && !state.maximized && sameBounds(state.normal, expectedBounds), 'restore normal size/position after restart');
  await assertVideoAndRange(page, settings, video);
  passed('process restart restores encrypted tokens, playing video, theme, navigation, search and normal geometry');

  await page.getByRole('button', { name: '最大化', exact: true }).click();
  const maximizedOutline = await waitWindow(session, state => state.found && state.maximized && state.shape?.regionType === 0, 'maximise without a clipping region');
  recordOutline('maximized', maximizedOutline);
  await page.getByRole('button', { name: '关闭窗口', exact: true }).click();
  await waitWindow(session, state => state.found && !state.visible, 'hide from maximised state');
  await restoreViaSecondInstance(session);
  await waitWindow(session, state => state.found && state.visible && state.maximized && sameBounds(state.normal, expectedBounds) && state.shape?.regionType === 0, 'remain maximised with square corners when restored from the tray');
  passed('maximised close-to-tray and second-instance restore preserve maximisation');
  await expectAppExit(session, 'QuitApp');
  await closeNative(session);
  const savedMaximized = JSON.parse(await readFile(path.join(dataDirectory, 'session.json'), 'utf8'));
  check(savedMaximized.window?.maximized && sameBounds(savedMaximized.window, expectedBounds), 'Maximised quit lost the maximised flag or normal restore rectangle');
  session = await launchNative(appData, localData);
  page = session.page;
  await assertIsolatedState(page, dataDirectory, 1);
  await waitWindow(session, state => state.found && state.visible && state.maximized && sameBounds(state.normal, expectedBounds) && state.shape?.regionType === 0, 'restore maximised state and normal geometry');
  await page.evaluate(() => window.runtime.WindowUnmaximise());
  const restoredOutline = await waitWindow(session, state => state.found && state.visible && !state.maximized && sameBounds(state.normal, expectedBounds) && hasRoundedOutline(state), 'return to saved normal geometry with rounded corners');
  recordOutline('restored normal', restoredOutline);
  passed('maximised close/quit/restart restores both maximised state and normal bounds');
  passed('native outline is square while maximized and rounded again after restoring');

  await page.evaluate(() => window.runtime.WindowFullscreen());
  await page.waitForFunction(async () => await window.runtime.WindowIsFullscreen());
  const fullscreenOutline = await waitWindow(session, state => state.found && state.visible && state.shape?.regionType === 0, 'clear the clipping region in fullscreen');
  recordOutline('fullscreen', fullscreenOutline);
  await page.evaluate(() => window.runtime.WindowUnfullscreen());
  await page.waitForFunction(async () => !(await window.runtime.WindowIsFullscreen()));
  const afterFullscreen = await waitWindow(session, state => state.found && state.visible && hasRoundedOutline(state) && sameBounds(state.normal, expectedBounds), 'restore rounded normal bounds after fullscreen');
  recordOutline('after fullscreen', afterFullscreen);
  passed('fullscreen clears the native corner region and exiting restores it');

  await bridge(page, 'DeleteToken', id);
  await assertIsolatedState(page, dataDirectory, 0);
  passed('fixture token deletion through the native bridge');
  settings.closeToTray = false;
  await bridge(page, 'SaveSettings', settings);
  await expectAppExit(session, 'CloseWindow');
  await closeNative(session);
  const savedSettings = JSON.parse(await readFile(path.join(dataDirectory, 'settings.json'), 'utf8'));
  check(savedSettings.closeToTray === false, 'Disabled close-to-tray preference was not saved');
  passed('CloseWindow exits when close-to-tray is disabled');

  check(createHash('sha256').update(await readFile(executable)).digest('hex') === productionHash, 'Production executable changed during native QA');
  passed('production executable SHA-256 unchanged');
  report = { status: 'passed', checks, outlines, screenshot, isolatedDirectory: isolatedRoot, testedExecutable: launchedExecutable, productionExecutable: executable, instrumentation: 'Test-only WebView2 CDP/profile hook in temporary dependency copy; unchanged application source and production binary', productionSHA256: productionHash, clipboardAccessed: false };
  console.log(`Native smoke passed (${checks.length} checks). Screenshot: ${screenshot}`);
} catch (error) {
  if (activeSession?.page && !activeSession.page.isClosed()) {
    await activeSession.page.screenshot({ path: path.join(output, 'native-failure.png'), fullPage: true }).catch(() => {});
  }
  report = { status: 'failed', checks, outlines, error: safeMessage(error), isolatedDirectory: isolatedRoot, testedExecutable: launchedExecutable, productionSHA256: productionHash, clipboardAccessed: false };
  console.error(`Native smoke failed: ${report.error}`);
  process.exitCode = 1;
} finally {
  for (const session of [...sessions]) {
    await closeNative(session).catch(error => {
      process.exitCode = 1;
      console.error(`Native cleanup failed: ${safeMessage(error)}`);
      if (report) report.cleanupError = safeMessage(error);
    });
  }
  if (report) {
    await mkdir(output, { recursive: true });
    await writeFile(path.join(output, 'native-report.json'), `${JSON.stringify(report, null, 2)}\n`);
  }
}
