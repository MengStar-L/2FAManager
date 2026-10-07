export type Token = { id: string; issuer: string; account: string; group: string; favorite: boolean; color: string; algorithm: string; digits: number; period: number; code: string; remaining: number }
export type TokenInput = { issuer: string; account: string; secret: string; group: string; color: string; algorithm: string; digits: number; period: number }
export type Settings = { backgroundType: 'pattern' | 'image' | 'video'; pattern: 'dots' | 'grid' | 'waves'; backgroundUrl: string; backgroundName: string; opacity: number; motion: boolean; theme: 'regular' | 'anime'; closeToTray: boolean; accentColor: string; checkUpdatesAutomatically: boolean; updateAutomatically: boolean }
export type UpdateStatus = { currentVersion: string; latestVersion: string; phase: 'idle' | 'checking' | 'available' | 'downloading' | 'ready' | 'upToDate' | 'error'; progress: number; releaseUrl: string; message: string; lastChecked: string; downloadedBytes: number; totalBytes: number }
export type SessionState = { sidebarCollapsed: boolean; filter: string; search: string }
export type ImportPreview = { issuer: string; account: string; uri: string }
export type State = { tokens: Token[]; settings: Settings; session: SessionState; dataPath: string; warning?: string }
export const defaultSettings: Settings = { backgroundType: 'pattern', pattern: 'dots', backgroundUrl: '', backgroundName: '', opacity: 0.18, motion: true, theme: 'regular', closeToTray: true, accentColor: '#8773b7', checkUpdatesAutomatically: true, updateAutomatically: false }
export const defaultUpdateStatus: UpdateStatus = { currentVersion: '', latestVersion: '', phase: 'idle', progress: 0, releaseUrl: '', message: '', lastChecked: '', downloadedBytes: 0, totalBytes: 0 }
export const defaultSession: SessionState = { sidebarCollapsed: false, filter: 'all', search: '' }
export const demo = new URLSearchParams(window.location.search).has('demo')
type Bridge = { GetState(): Promise<State>; GetTokens(): Promise<Token[]>; PreviewClipboard(): Promise<ImportPreview[]>; PreviewText(text: string): Promise<ImportPreview[]>; PreviewImage(image: string): Promise<ImportPreview[]>; ImportTokens(uris: string[]): Promise<Token[]>; AddToken(input: TokenInput): Promise<Token>; UpdateToken(id: string, input: TokenInput): Promise<Token>; DeleteToken(id: string): Promise<void>; ToggleFavorite(id: string): Promise<void>; CopyCode(id: string): Promise<void>; PickBackground(): Promise<Settings | null>; SaveSettings(settings: Settings): Promise<Settings>; SaveSession(session: SessionState): Promise<void>; CloseWindow(): Promise<void>; QuitApp(): Promise<void>; GetUpdateStatus(): Promise<UpdateStatus>; CheckForUpdates(): Promise<UpdateStatus>; DownloadUpdate(): Promise<UpdateStatus>; InstallUpdate(): Promise<void>; OpenReleasePage(): Promise<void> }
type RuntimeEvents = { (event: 'app:warning', callback: (message: string) => void): () => void; (event: 'app:update', callback: (status: UpdateStatus) => void): () => void }
declare global { interface Window { go?: { main?: { App?: Bridge } }; runtime?: { WindowMinimise(): void; WindowToggleMaximise(): void; WindowIsMaximised?(): Promise<boolean>; EventsOn?: RuntimeEvents; Quit(): void } } }
export const native = Boolean(window.go?.main?.App)
let mockSettings = { ...defaultSettings }
let mockUpdate: UpdateStatus = { ...defaultUpdateStatus, currentVersion: '0.3.0' }
let mockDownloadTimer: ReturnType<typeof setInterval> | null = null
const mockUpdateListeners = new Set<(status: UpdateStatus) => void>()
function publishMockUpdate(status: Partial<UpdateStatus>) { mockUpdate = { ...mockUpdate, ...status }; for (const listener of mockUpdateListeners) listener({ ...mockUpdate }); return { ...mockUpdate } }
export function subscribeToUpdates(listener: (status: UpdateStatus) => void): () => void {
  if (native) return window.runtime?.EventsOn?.('app:update', listener) || (() => {})
  mockUpdateListeners.add(listener)
  return () => { mockUpdateListeners.delete(listener) }
}
let mockSession = { ...defaultSession }
try {
  const saved = JSON.parse(localStorage.getItem('luma.session') || 'null') as Partial<SessionState> | null
  mockSession = { sidebarCollapsed: saved?.sidebarCollapsed ?? localStorage.getItem('luma.sidebarCollapsed') === 'true', filter: typeof saved?.filter === 'string' ? saved.filter : 'all', search: typeof saved?.search === 'string' ? saved.search : '' }
} catch { /* A damaged optional preview preference should not prevent loading. */ }
let mockTokens: Token[] = demo ? [
  ['GitHub', 'hello@luma.design', '工作', '#8581d8', true, '348291'],
  ['Google', 'hello@luma.design', '个人', '#629bc9', true, '620845'],
  ['Figma', 'design@luma.studio', '工作', '#cb899f', false, '193706'],
  ['Notion', 'hello@luma.design', '工作', '#9a998f', false, '852419'],
  ['Microsoft', 'me@outlook.com', '个人', '#71a594', false, '467082'],
  ['Discord', 'luma • personal', '个人', '#9990c6', false, '709534'],
].map(([issuer, account, group, color, favorite, code], i) => ({ id: `demo-${i}`, issuer: String(issuer), account: String(account), group: String(group), color: String(color), favorite: Boolean(favorite), algorithm: 'SHA1', digits: 6, period: 30, code: String(code), remaining: 30 })) : []
const refreshDemo = () => mockTokens.map(t => ({ ...t, remaining: t.period - Math.floor(Date.now() / 1000) % t.period }))
function requireDemo() { if (!demo) throw new Error('请在桌面应用中使用此功能。浏览器仅用于界面预览。') }
function previewText(text: string): ImportPreview[] {
  const lines = text.trim().split(/\s+/).filter(Boolean)
  if (!lines.length) throw new Error('请先粘贴 otpauth:// 令牌链接。')
  return lines.map(line => {
    const url = new URL(line)
    if (url.protocol !== 'otpauth:' || url.hostname !== 'totp' || !url.searchParams.get('secret')) throw new Error('请输入有效的 TOTP 令牌链接。')
    const label = decodeURIComponent(url.pathname.slice(1)); const split = label.indexOf(':')
    return { issuer: url.searchParams.get('issuer') || (split >= 0 ? label.slice(0, split) : '未命名'), account: split >= 0 ? label.slice(split + 1) : label, uri: line }
  })
}
const mock: Bridge = {
  async GetState() { return { tokens: refreshDemo(), settings: mockSettings, session: mockSession, dataPath: '' } },
  async GetTokens() { return refreshDemo() },
  async PreviewClipboard() { requireDemo(); throw new Error('真实剪贴板二维码识别请使用桌面应用。演示模式可测试令牌链接和手动添加。') },
  async PreviewText(text) { requireDemo(); return previewText(text) },
  async PreviewImage() { requireDemo(); throw new Error('二维码图片识别需要桌面应用。演示模式可测试手动添加和令牌链接。') },
  async ImportTokens(uris) { requireDemo(); const previews = uris.flatMap(previewText); for (const p of previews) { const u = new URL(p.uri); await mock.AddToken({ issuer: p.issuer, account: p.account, secret: u.searchParams.get('secret') || '', group: '', color: '#8581d8', algorithm: u.searchParams.get('algorithm') || 'SHA1', digits: Number(u.searchParams.get('digits') || 6), period: Number(u.searchParams.get('period') || 30) }) } return refreshDemo() },
  async AddToken(input) { requireDemo(); if (!/^[A-Z2-7]+=*$/i.test(input.secret.replace(/\s/g, ''))) throw new Error('密钥需要使用 Base32 格式（A–Z、2–7）。'); const t: Token = { id: crypto.randomUUID(), issuer: input.issuer, account: input.account, group: input.group, color: input.color, algorithm: input.algorithm, digits: input.digits, period: input.period, favorite: false, code: '123456'.padEnd(input.digits, '0'), remaining: input.period }; mockTokens = [...mockTokens, t]; return t },
  async UpdateToken(id, input) { requireDemo(); mockTokens = mockTokens.map(t => t.id === id ? { ...t, issuer: input.issuer, account: input.account, group: input.group, color: input.color, algorithm: input.algorithm, digits: input.digits, period: input.period, code: '123456'.padEnd(input.digits, '0') } : t); return mockTokens.find(t => t.id === id)! },
  async DeleteToken(id) { requireDemo(); mockTokens = mockTokens.filter(t => t.id !== id) },
  async ToggleFavorite(id) { requireDemo(); mockTokens = mockTokens.map(t => t.id === id ? { ...t, favorite: !t.favorite } : t) },
  async CopyCode(id) { requireDemo(); const t = mockTokens.find(t => t.id === id); if (t) await navigator.clipboard.writeText(t.code) },
  async PickBackground() { requireDemo(); throw new Error('本地图片 / 视频背景选择需要桌面应用。') },
  async SaveSettings(settings) { requireDemo(); mockSettings = { ...settings }; if (settings.updateAutomatically && mockUpdate.phase === 'available') void mock.DownloadUpdate(); return mockSettings },
  async SaveSession(session) { mockSession = { ...session }; try { localStorage.setItem('luma.session', JSON.stringify(session)); localStorage.setItem('luma.sidebarCollapsed', String(session.sidebarCollapsed)) } catch { /* Browser layout preferences are optional. */ } },
  async CloseWindow() { throw new Error('窗口和托盘功能请在桌面应用中使用。') },
  async QuitApp() { throw new Error('退出功能请在桌面应用中使用。') },
  async GetUpdateStatus() { return { ...mockUpdate } },
  async CheckForUpdates() { requireDemo(); if (mockUpdate.phase === 'downloading' || mockUpdate.phase === 'ready') return { ...mockUpdate }; publishMockUpdate({ phase: 'checking', message: '' }); await new Promise(resolve => setTimeout(resolve, 320)); const phase = mockUpdate.currentVersion === '0.3.1' ? 'upToDate' : 'available'; const status = publishMockUpdate({ phase, latestVersion: '0.3.1', lastChecked: new Date().toISOString(), releaseUrl: '', message: '演示更新，不会下载或安装真实文件。' }); if (phase === 'available' && mockSettings.updateAutomatically) return mock.DownloadUpdate(); return status },
  async DownloadUpdate() { requireDemo(); if (mockDownloadTimer || mockUpdate.phase === 'ready') return { ...mockUpdate }; if (!mockUpdate.latestVersion || mockUpdate.latestVersion === mockUpdate.currentVersion) throw new Error('请先检查更新。'); publishMockUpdate({ phase: 'downloading', progress: 0, downloadedBytes: 0, totalBytes: 24 * 1024 * 1024, message: '正在模拟下载…' }); mockDownloadTimer = setInterval(() => { const progress = Math.min(100, mockUpdate.progress + 12.5); publishMockUpdate({ phase: progress === 100 ? 'ready' : 'downloading', progress, downloadedBytes: mockUpdate.totalBytes * progress / 100, message: progress === 100 ? '演示更新已就绪，不会修改真实文件。' : '正在模拟下载…' }); if (progress === 100 && mockDownloadTimer) { clearInterval(mockDownloadTimer); mockDownloadTimer = null } }, 180); return { ...mockUpdate } },
  async InstallUpdate() { requireDemo(); if (mockUpdate.phase !== 'ready') throw new Error('更新尚未下载完成。'); publishMockUpdate({ currentVersion: mockUpdate.latestVersion, phase: 'upToDate', progress: 0, downloadedBytes: 0, totalBytes: 0, message: '演示更新已完成，浏览器不会重启。' }) },
  async OpenReleasePage() { throw new Error('发布说明请在桌面应用中打开。') },
}
export const api: Bridge = window.go?.main?.App || mock
