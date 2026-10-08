import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowLeft, ArrowRight, Check, CheckCheck, ChevronDown, CircleHelp, Clipboard, Copy, Ellipsis, Fingerprint, IconTheme, ImagePlus, KeyRound, LayoutGrid, LockKeyhole, Maximize2, Minus, Palette, PanelLeftClose, PanelLeftOpen, Plus, QrCode, Search, Settings2, ShieldCheck, Star, Trash2, Upload, Video, X } from './themed-icons'
import { api, defaultSession, defaultSettings, demo, native, type ImportPreview, type SessionState, type Settings, type Token, type TokenInput } from './api'
import appIcon from './assets/images/app-icon.png'
import { ProviderIcon } from './ProviderIcon'
import { UpdateSettings } from './UpdateSettings'
import { useUpdates } from './useUpdates'
import { AccentPicker } from './AccentPicker'
import { accentStyle } from './colorTheme'
import { AnimatedPanel } from './AnimatedPanel'
import { ImportPanel } from './ImportPanel'
import { mergeImportPreviews, migrationProgress } from './importCollection'
import { dialogExitDuration, useDialogPresence } from './useDialogPresence'
import './App.css'
import './layout.css'
import './accent.css'
import './updates.css'
import './token-cards.css'

type Modal = 'import' | 'manual' | 'appearance' | 'help' | null
const colors = ['#8581d8', '#629bc9', '#cb899f', '#71a594', '#caa16a', '#9a998f']
const emptyInput: TokenInput = { issuer: '', account: '', secret: '', group: '', color: colors[0], algorithm: 'SHA1', digits: 6, period: 30 }
const message = (e: unknown) => e instanceof Error ? e.message : String(e)
const splitCode = (code: string) => code ? `${code.slice(0, Math.ceil(code.length / 2))} ${code.slice(Math.ceil(code.length / 2))}` : '••• •••'

function App() {
  const [tokens, setTokens] = useState<Token[]>([])
  const [settings, setSettings] = useState<Settings>(defaultSettings)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [filter, setFilter] = useState('all')
  const [query, setQuery] = useState('')
  const [modal, setModal] = useState<Modal>(null)
  const [editing, setEditing] = useState<Token | null>(null)
  const [deleting, setDeleting] = useState<Token | null>(null)
  const [input, setInput] = useState<TokenInput>(emptyInput)
  const [uri, setUri] = useState('')
  const [previews, setPreviews] = useState<ImportPreview[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [toast, setToast] = useState('')
  const [copied, setCopied] = useState('')
  const [copiedAccount, setCopiedAccount] = useState('')
  const [menu, setMenu] = useState('')
  const [advanced, setAdvanced] = useState(false)
  const [draft, setDraft] = useState<Settings>(defaultSettings)
  const [maximized, setMaximized] = useState(false)
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [sessionLoaded, setSessionLoaded] = useState(false)
  const [searchOpen, setSearchOpen] = useState(false)
  const [reducedMotion, setReducedMotion] = useState(() => window.matchMedia('(prefers-reduced-motion: reduce)').matches)
  const busyRef = useRef(false)
  const searchRef = useRef<HTMLInputElement>(null)
  const dialogRef = useRef<HTMLDivElement>(null)
  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const copiedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const accountTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const previousFocus = useRef<HTMLElement | null>(null)
  const tabErrors = useRef({ import: '', manual: '' })
  const dialogOpen = Boolean(modal || deleting)
  const dialogMotion = (modal === 'appearance' ? draft.motion : settings.motion) && !reducedMotion
  const resetDialog = useCallback(() => { setModal(null); setDeleting(null); setError(''); setPreviews([]); setUri(''); setInput(emptyInput); setEditing(null); setAdvanced(false); tabErrors.current = { import: '', manual: '' } }, [])
  const { closing, close: beginModalExit, cancel: cancelModalExit, finish: finishModalExit } = useDialogPresence(dialogMotion, resetDialog)
  const completeModal = useCallback(() => beginModalExit(() => { const dialog = dialogRef.current; const overlay = dialog?.parentElement; if (!dialog || !overlay) return; const current = getComputedStyle(dialog); dialog.style.setProperty('--exit-from-opacity', current.opacity); dialog.style.setProperty('--exit-from-transform', current.transform); overlay.style.setProperty('--exit-from-opacity', getComputedStyle(overlay).opacity) }), [beginModalExit])
  const sessionQueue = useRef<Promise<void>>(Promise.resolve())
  const searchButtonRef = useRef<HTMLButtonElement>(null)
  useEffect(() => { const media = window.matchMedia('(prefers-reduced-motion: reduce)'); const update = () => setReducedMotion(media.matches); media.addEventListener('change', update); return () => media.removeEventListener('change', update) }, [])
  const notify = useCallback((text: string) => { setToast(text); if (toastTimer.current) clearTimeout(toastTimer.current); toastTimer.current = setTimeout(() => setToast(''), 4000) }, [])
  const updates = useUpdates(notify, settings.updateAutomatically)
  useEffect(() => window.runtime?.EventsOn?.('app:warning', notify), [notify])
  const reload = useCallback(async () => { setTokens((await api.GetTokens()) || []) }, [])
  const load = useCallback(async () => { setLoading(true); setLoadError(''); try { const state = await api.GetState(); setTokens(state.tokens || []); setSettings({ ...defaultSettings, ...state.settings }); const session = { ...defaultSession, ...state.session }; const restoredFilter = session.filter === 'all' || session.filter === 'favorites' || (session.filter.startsWith('group:') && state.tokens?.some(t => t.group === session.filter.slice(6))) ? session.filter : 'all'; setSidebarCollapsed(session.sidebarCollapsed); setFilter(restoredFilter); setQuery(session.search); setSearchOpen(Boolean(session.search)); setSessionLoaded(true); if (state.warning) notify(state.warning) } catch (e) { setLoadError(message(e)) } finally { setLoading(false) } }, [notify])
  const saveSession = useCallback((session: SessionState) => { const next = sessionQueue.current.catch(() => {}).then(() => api.SaveSession(session)); sessionQueue.current = next; return next }, [])
  useEffect(() => { if (!sessionLoaded) return; void saveSession({ sidebarCollapsed, filter, search: query }).catch(e => notify(`界面状态未保存：${message(e)}`)) }, [sidebarCollapsed, filter, query, sessionLoaded, saveSession, notify])
  useEffect(() => { if (!searchOpen) return; const timer = setTimeout(() => searchRef.current?.focus(), 30); return () => clearTimeout(timer) }, [searchOpen])
  useEffect(() => { const update = () => { void window.runtime?.WindowIsMaximised?.().then(setMaximized) }; update(); window.addEventListener('resize', update); return () => window.removeEventListener('resize', update) }, [])
  const closeWindow = async () => { try { if (sessionLoaded) await saveSession({ sidebarCollapsed, filter, search: query }); await api.CloseWindow() } catch (e) { notify(message(e)) } }
  function toggleSearch() { if (searchOpen && !query) { setSearchOpen(false); searchButtonRef.current?.focus() } else { setSearchOpen(true); searchRef.current?.focus() } }
  useEffect(() => { void load(); const timer = setInterval(() => { void api.GetTokens().then(v => { setTokens(v || []); setLoadError('') }).catch(e => setLoadError(message(e))) }, 1000); return () => { clearInterval(timer); if (toastTimer.current) clearTimeout(toastTimer.current) } }, [load])
  const closeModal = useCallback(() => { if (!busy && !busyRef.current) completeModal() }, [busy, completeModal])
  const rememberDialogFocus = useCallback(() => { if (dialogOpen) return; const focused = document.activeElement as HTMLElement; previousFocus.current = focused.closest('.card-menu-anchor')?.querySelector<HTMLElement>('.more-button') || focused }, [dialogOpen])
  function openModal(next: Modal) { rememberDialogFocus(); cancelModalExit(); tabErrors.current = { import: '', manual: '' }; setError(''); setPreviews([]); setUri(''); setEditing(null); setInput(emptyInput); setAdvanced(false); setMenu(''); setDraft(settings); setModal(next) }
  function switchTokenMode(next: 'import' | 'manual') { if (busy || closing || modal === next) return; if (modal === 'import' || modal === 'manual') tabErrors.current[modal] = error; setError(tabErrors.current[next]); setModal(next) }
  const run = useCallback(async (fn: () => Promise<void>) => { if (busyRef.current) return; busyRef.current = true; setBusy(true); setError(''); try { await fn() } catch (e) { setError(message(e)) } finally { busyRef.current = false; setBusy(false) } }, [])
  const pasteClipboard = useCallback(() => run(async () => { rememberDialogFocus(); cancelModalExit(); setModal('import'); const results = await api.PreviewClipboard(); if (!results?.length) throw new Error('剪贴板中没有可导入的令牌，请复制二维码图片或令牌链接后重试。'); setPreviews(mergeImportPreviews(previews, results)) }), [cancelModalExit, rememberDialogFocus, run, previews])
  useEffect(() => {
    const onPaste = (e: ClipboardEvent) => { const el = e.target as HTMLElement; if (el.closest('input,textarea,[contenteditable="true"]') || modal || deleting || busy) return; e.preventDefault(); void pasteClipboard() }
    const onKey = (e: KeyboardEvent) => { const el = e.target as HTMLElement; if (e.key === '/' && !el.closest('input,textarea,[contenteditable="true"]') && !modal && !deleting) { e.preventDefault(); setSearchOpen(true); searchRef.current?.focus() } }
    document.addEventListener('paste', onPaste); document.addEventListener('keydown', onKey); return () => { document.removeEventListener('paste', onPaste); document.removeEventListener('keydown', onKey) }
  }, [pasteClipboard, modal, deleting, busy])
  useEffect(() => {
    if (!dialogOpen) return
    const returnFocus = previousFocus.current || document.activeElement as HTMLElement
    previousFocus.current = returnFocus
    const timer = setTimeout(() => dialogRef.current?.querySelector<HTMLElement>('input,button,textarea,select')?.focus(), 50)
    return () => { clearTimeout(timer); if (returnFocus.isConnected) returnFocus.focus(); else document.querySelector<HTMLElement>('.tool-dock [aria-label="添加令牌"]')?.focus(); previousFocus.current = null }
  }, [dialogOpen])
  useEffect(() => {
    if (!modal && !deleting) return
    const handler = (e: KeyboardEvent) => {
      if (closing) { if (e.key === 'Escape' || e.key === 'Tab') e.preventDefault(); return }
      if (e.key === 'Escape') closeModal()
      if (e.key !== 'Tab') return
      const elements = [...(dialogRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),textarea:not(:disabled),select:not(:disabled),[tabindex="0"]') || [])].filter(el => el.offsetParent !== null && el.tabIndex >= 0)
      const first = elements[0]; const last = elements[elements.length - 1]
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last?.focus() } else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first?.focus() }
    }
    document.addEventListener('keydown', handler); return () => document.removeEventListener('keydown', handler)
  }, [modal, deleting, closeModal, closing])
  useEffect(() => { const close = () => setMenu(''); if (menu) { document.addEventListener('click', close); return () => document.removeEventListener('click', close) } }, [menu])
  useEffect(() => () => { if (copiedTimer.current) clearTimeout(copiedTimer.current); if (accountTimer.current) clearTimeout(accountTimer.current) }, [])
  const copyToken = async (token: Token) => { try { await api.CopyCode(token.id); setCopied(token.id); notify('验证码已复制'); if (copiedTimer.current) clearTimeout(copiedTimer.current); copiedTimer.current = setTimeout(() => setCopied(''), 2200) } catch (e) { notify(message(e)) } }
  const copyAccount = async (token: Token) => { try { await api.CopyAccount(token.id); setCopiedAccount(token.id); notify(token.account.includes('@') ? '邮箱已复制' : '账户已复制'); if (accountTimer.current) clearTimeout(accountTimer.current); accountTimer.current = setTimeout(() => setCopiedAccount(''), 2200) } catch (e) { notify(message(e)) } }
  const favorite = async (token: Token) => { try { await api.ToggleFavorite(token.id); await reload() } catch (e) { notify(message(e)) } }
  function edit(token: Token) { openModal('manual'); setEditing(token); setInput({ issuer: token.issuer, account: token.account, secret: '', group: token.group, color: token.color || colors[0], algorithm: token.algorithm, digits: token.digits, period: token.period }) }
  async function readImages(files: File[]) {
    if (!files.length) return
    await run(async () => {
      if (files.length > 100) throw new Error('一次最多读取 100 张二维码图片。')
      let next = previews
      for (const file of files) {
        if (file.size > 20 * 1024 * 1024) throw new Error('请选择小于 20 MB 的二维码图片。')
        const encoded = await new Promise<string>((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result).split(',')[1]); reader.onerror = () => reject(new Error('无法读取图片')); reader.readAsDataURL(file) })
        const result = await api.PreviewImage(encoded)
        if (!result?.length) throw new Error('图片中没有找到可导入的令牌二维码。')
        next = mergeImportPreviews(next, result)
      }
      setPreviews(next)
    })
  }
  const readImportText = () => run(async () => { const result = await api.PreviewText(uri); setPreviews(mergeImportPreviews(previews, result || [])); setUri('') })
  const importTokens = () => run(async () => {
    if (!previews.length || migrationProgress(previews).some(batch => !batch.complete)) throw new Error('请先读取这组迁移的全部二维码。')
    await api.ImportTokens([...new Set(previews.map(preview => preview.uri))])
    await reload(); completeModal(); setFilter('all'); setQuery(''); notify('导入成功')
  })
  const groups = [...new Set(tokens.map(t => t.group).filter(Boolean))].sort()
  useEffect(() => { if (sessionLoaded && filter.startsWith('group:') && !tokens.some(t => t.group === filter.slice(6))) setFilter('all') }, [tokens, filter, sessionLoaded])
  const favorites = tokens.filter(t => t.favorite).length
  const selectedGroup = filter.startsWith('group:') ? filter.slice(6) : ''
  const visible = tokens.filter(t => (filter === 'all' || (filter === 'favorites' ? t.favorite : t.group === selectedGroup)) && `${t.issuer} ${t.account} ${t.group}`.toLowerCase().includes(query.toLowerCase()))
  const activeSettings = modal === 'appearance' ? draft : settings
  const realBackground = activeSettings.backgroundType !== 'pattern' && activeSettings.backgroundUrl

  return <IconTheme.Provider value={activeSettings.theme}><div className={`app theme-${activeSettings.theme} ${activeSettings.motion ? '' : 'still'} ${sidebarCollapsed ? 'sidebar-collapsed' : ''}`} style={accentStyle(activeSettings.accentColor)}>
    <div className={`wallpaper pattern-${activeSettings.pattern}`} aria-hidden="true">{realBackground && (activeSettings.backgroundType === 'video' ? <video src={activeSettings.backgroundUrl} autoPlay={activeSettings.motion && !reducedMotion} muted loop playsInline onError={() => notify('视频无法播放，请尝试 H.264 MP4 或 WebM 格式。')} style={{ opacity: activeSettings.opacity }} key={`${activeSettings.backgroundUrl}-${activeSettings.motion}-${reducedMotion}`} /> : <img src={activeSettings.backgroundUrl} onError={() => notify('背景图片无法显示，请重新选择图片。')} style={{ opacity: activeSettings.opacity }} alt="" />)}<div className="wallpaper-glow glow-one" /><div className="wallpaper-glow glow-two" /></div>
    <header className="titlebar"><span className="titlebar-wordmark"><img src={appIcon} alt="" /> Luma</span><div className="window-actions"><button aria-label="最小化" title="最小化" onClick={() => window.runtime?.WindowMinimise()}><Minus size={15} /></button><button aria-label={maximized ? '还原窗口' : '最大化'} title={maximized ? '还原窗口' : '最大化'} onClick={() => { window.runtime?.WindowToggleMaximise(); setMaximized(!maximized) }}><Maximize2 size={12} /></button><button aria-label="关闭窗口" title={settings.closeToTray ? '收起到托盘' : '退出'} className="window-close" onClick={() => void closeWindow()}><X size={16} /></button></div></header>
    <div className="workspace"><aside className="sidebar">
      <div className="sidebar-header"><a className="brand" href="#" onClick={e => { e.preventDefault(); setFilter('all'); setQuery('') }} aria-label="Luma 首页"><span className="brand-symbol"><img src={appIcon} alt="" /></span><span className="brand-label"><strong>Luma</strong></span></a><button className="sidebar-toggle" aria-label={sidebarCollapsed ? '展开侧栏' : '收起侧栏'} title={sidebarCollapsed ? '展开侧栏' : '收起侧栏'} aria-expanded={!sidebarCollapsed} aria-controls="sidebar-navigation" onClick={() => setSidebarCollapsed(v => !v)}>{sidebarCollapsed ? <PanelLeftOpen size={19} /> : <PanelLeftClose size={19} />}</button></div>
      <div id="sidebar-navigation" className="sidebar-navigation"><nav className="main-nav" aria-label="令牌导航"><button aria-label="所有令牌" title="所有令牌" className={filter === 'all' ? 'active' : ''} onClick={() => setFilter('all')}><LayoutGrid size={18} /><span className="nav-text">所有令牌</span><b>{tokens.length.toString().padStart(2, '0')}</b></button><button aria-label="星标收藏" title="星标收藏" className={filter === 'favorites' ? 'active' : ''} onClick={() => setFilter('favorites')}><Star size={18} /><span className="nav-text">星标收藏</span><b>{favorites.toString().padStart(2, '0')}</b></button></nav>
      {groups.length > 0 && <><div className="nav-label group-label">分组</div><nav className="group-nav" aria-label="令牌分组">{groups.map((g, i) => <button key={g} aria-label={g} title={g} className={selectedGroup === g ? 'active' : ''} onClick={() => setFilter('group:' + g)}><span className="group-dot" style={{ background: colors[i % colors.length] }} /><span className="group-initial" aria-hidden="true">{g.slice(0, 1)}</span><span className="nav-text">{g}</span><small>{tokens.filter(t => t.group === g).length}</small></button>)}</nav></>}</div>
      <div className="sidebar-bottom"><button className="appearance-link" data-update={updates.status.phase === 'available' || updates.status.phase === 'ready' ? 'true' : undefined} aria-label="外观与背景" title="外观与背景" onClick={() => openModal('appearance')}><Palette size={18} /><span className="nav-text">外观与背景</span><ArrowRight size={15} /></button><button className="help-link" aria-label="使用帮助" title="使用帮助" onClick={() => openModal('help')}><CircleHelp size={18} /><span className="nav-text">使用帮助</span></button></div>
    </aside><main className="main-content">
      {!native && <div className="preview-banner"><span>{demo ? '演示模式 · 示例验证码不可用于登录，数据不保存' : '浏览器预览 · 完整功能请使用桌面应用'}</span>{!demo && <a href="?demo">查看示例 <ArrowRight size={13} /></a>}</div>}
      <div className={`toolbar ${searchOpen ? 'search-open' : ''}`}><div className="tool-dock"><button ref={searchButtonRef} className={`icon-action ${searchOpen ? 'selected' : ''}`} aria-label="搜索" title="搜索 /" aria-expanded={searchOpen} aria-controls="token-search" onClick={toggleSearch}><Search size={19} /></button><button className="icon-action paste-top" aria-label="粘贴二维码" title="粘贴二维码 Ctrl + V" onClick={() => void pasteClipboard()} disabled={busy}><Clipboard size={19} /></button><button className="icon-action primary-action" aria-label="添加令牌" title="添加令牌" onClick={() => openModal('import')}><Plus size={21} /></button></div>{searchOpen && <label className="search-box" id="token-search"><input ref={searchRef} aria-label="搜索令牌" maxLength={512} value={query} onChange={e => setQuery(e.target.value)} onKeyDown={e => { if (e.key === 'Escape') { setQuery(''); setSearchOpen(false); searchButtonRef.current?.focus() } }} placeholder="搜索名称或账户…" />{query && <button aria-label="清除搜索" title="清除搜索" onClick={() => { setQuery(''); searchRef.current?.focus() }}><X size={14} /></button>}<button aria-label="收起搜索" title="收起搜索" onClick={() => { setQuery(''); setSearchOpen(false); searchButtonRef.current?.focus() }}><ArrowLeft size={15} /></button></label>}</div>
      {loadError && <div role="alert" className="load-error">{loadError}<button onClick={() => void load()}>重试</button></div>}
      {loading ? <div className="loading-state"><span className="loader" />正在加载…</div> : visible.length ? <div className="token-grid">{visible.map((token, i) => <article className={`token-card ${copied === token.id ? 'is-copied' : ''} ${token.digits === 8 ? 'eight-digits' : ''}`} key={token.id} style={{ '--accent': token.color || colors[i % colors.length], '--delay': `${Math.min(i, 10) * 45}ms` } as React.CSSProperties}>
        <div className="card-heading"><div className="provider-icon"><ProviderIcon issuer={token.issuer} account={token.account} /></div><div className="card-label"><h2 title={token.issuer}>{token.issuer || '未命名令牌'}</h2>{token.account ? <button className={`account-copy ${copiedAccount === token.id ? 'is-account-copied' : ''}`} title={`复制${token.account.includes('@') ? '邮箱' : '账户'}：${token.account}`} aria-label={`复制${token.account.includes('@') ? '邮箱' : '账户'} ${token.account}`} onClick={() => void copyAccount(token)}><span>{token.account}</span><span className="account-copy-icon" aria-hidden="true">{copiedAccount === token.id ? <Check size={12} /> : <Copy size={12} />}</span></button> : <span className="account-empty">个人账户</span>}</div><button className={`star-button ${token.favorite ? 'is-starred' : ''}`} aria-label={`${token.favorite ? '取消星标' : '收藏'} ${token.issuer}`} aria-pressed={token.favorite} onClick={() => void favorite(token)}><Star size={16} fill={token.favorite ? 'currentColor' : 'none'} /></button></div>
        <button className="code-button" onClick={() => void copyToken(token)} aria-label={`复制 ${token.issuer} 验证码`}><span className="token-code">{splitCode(token.code)}</span><span className="copy-icon">{copied === token.id ? <CheckCheck size={18} /> : <Copy size={17} />}</span></button>
        <div className="card-bottom"><span className="group-tag">{token.group || '未分组'}</span><div className={`countdown ${token.remaining <= 5 ? 'expiring' : ''}`}><svg viewBox="0 0 20 20" aria-hidden="true"><circle className="ring-track" cx="10" cy="10" r="7" /><circle className="ring-progress" cx="10" cy="10" r="7" strokeDasharray="44" strokeDashoffset={44 * (1 - token.remaining / token.period)} /></svg><span>{token.remaining}s</span></div><div className="card-menu-anchor"><button className="more-button" aria-label={`${token.issuer} 更多操作`} aria-expanded={menu === token.id} onClick={e => { e.stopPropagation(); setMenu(menu === token.id ? '' : token.id) }}><Ellipsis size={19} /></button>{menu === token.id && <div className="card-menu"><button onClick={() => edit(token)}><Settings2 size={14} />编辑令牌</button><button className="danger-text" onClick={() => { rememberDialogFocus(); cancelModalExit(); setDeleting(token); setError('') }}><Trash2 size={14} />删除令牌</button></div>}</div></div><div className="card-time-track"><span style={{ width: `${100 * token.remaining / token.period}%` }} /></div>
      </article>)}<button className="add-card" onClick={() => openModal('import')}><span className="add-card-circle"><Plus size={23} strokeWidth={1.4} /></span><span>添加新令牌</span></button></div> : <section className="empty-state"><div className="empty-art"><div className="orbit orbit-one" /><div className="orbit orbit-two" /><span className="floating-spark spark-a">✳</span><span className="floating-spark spark-b">✦</span><div className="empty-mini-card mini-card-back" /><div className="empty-mini-card mini-card-front"><span className="mini-provider"><KeyRound size={21} /></span><span className="mini-line" /><strong>✦ ✦ ✦ <span>✦ ✦ ✦</span></strong><span className="mini-progress" /></div><span className="floating-lock"><ShieldCheck size={20} /></span></div><h2>{query ? '没有匹配的令牌' : filter === 'favorites' ? '暂无收藏' : '暂无令牌'}</h2>{query ? <button className="btn btn-secondary" onClick={() => setQuery('')}>清除搜索</button> : filter === 'favorites' ? <button className="btn btn-secondary" onClick={() => setFilter('all')}>查看所有令牌 <ArrowRight size={15} /></button> : <div className="empty-actions"><button className="btn btn-primary" onClick={() => void pasteClipboard()}><Clipboard size={17} />从剪贴板导入</button><button className="text-button" onClick={() => openModal('manual')}>手动添加 <ArrowRight size={15} /></button></div>}</section>}

    </main></div>
    {dialogOpen && <div className={`modal-overlay ${closing ? 'is-closing' : ''}`} style={{ '--dialog-exit-duration': `${dialogExitDuration}ms` } as React.CSSProperties} onMouseDown={e => { if (e.target === e.currentTarget) closeModal() }}><div className={`modal ${modal === 'appearance' ? 'appearance-modal' : ''} ${deleting ? 'delete-modal' : ''} ${closing ? 'is-closing' : ''}`} onAnimationEnd={e => { if (e.target === e.currentTarget && e.animationName === 'modalOut') finishModalExit() }} onKeyDownCapture={e => { if (closing) { e.preventDefault(); e.stopPropagation() } }} onClickCapture={e => { if (closing) { e.preventDefault(); e.stopPropagation() } }} role="dialog" aria-modal="true" aria-labelledby="dialog-title" ref={dialogRef}><button className="modal-close" aria-label="关闭弹窗" onClick={closeModal} disabled={busy || closing}><X size={20} /></button>
      {deleting ? <><span className="modal-emblem danger-emblem"><Trash2 size={24} /></span><h2 id="dialog-title">删除令牌？</h2><p className="modal-description">将从本机删除 <strong>{deleting.issuer}</strong> 的令牌。请先确认你有其他验证方式或恢复码，以免无法登录。</p><div className="delete-account">{deleting.account}</div>{error && <p role="alert" className="form-error">{error}</p>}<div className="modal-footer"><button className="btn btn-secondary" onClick={closeModal} disabled={busy}>取消</button><button className="btn btn-danger" disabled={busy} onClick={() => void run(async () => { await api.DeleteToken(deleting.id); await reload(); completeModal(); notify('令牌已删除') })}>{busy ? '正在删除…' : '确认删除'}</button></div></> : modal === 'appearance' ? <>
        <h2 id="dialog-title">设置</h2>
        <UpdateSettings draft={draft} changeDraft={setDraft} unsaved={JSON.stringify(draft) !== JSON.stringify(settings)} {...updates} />
        <section className="settings-section theme-section"><h3>图标与字体</h3><div className="theme-options" role="radiogroup" aria-label="图标与字体主题" onKeyDown={e => { if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End'].includes(e.key)) return; e.preventDefault(); const theme = e.key === 'Home' ? 'regular' : e.key === 'End' ? 'anime' : draft.theme === 'regular' ? 'anime' : 'regular'; setDraft({ ...draft, theme }); e.currentTarget.querySelectorAll<HTMLButtonElement>('button')[theme === 'regular' ? 0 : 1]?.focus() }}>
          {(['regular', 'anime'] as const).map(theme => <button key={theme} role="radio" tabIndex={draft.theme === theme ? 0 : -1} aria-checked={draft.theme === theme} aria-label={theme === 'regular' ? '常规主题' : '二次元主题'} className={`theme-option theme-preview-${theme} ${draft.theme === theme ? 'selected' : ''}`} onClick={() => setDraft({ ...draft, theme })}><IconTheme.Provider value={theme}><span className="theme-preview-icons"><LayoutGrid size={19} /><Star size={20} /><Clipboard size={19} /></span></IconTheme.Provider><span className="theme-option-label">{theme === 'regular' ? '常规' : '二次元'}</span>{draft.theme === theme && <Check className="theme-check" size={15} />}</button>)}
        </div></section>
        <AccentPicker value={draft.accentColor} onChange={accentColor => setDraft(current => ({ ...current, accentColor }))} />
        <section className="settings-section"><h3>默认纹理</h3><div className="pattern-options">{(['dots', 'grid', 'waves'] as const).map((pattern, i) => <button key={pattern} aria-pressed={draft.backgroundType === 'pattern' && draft.pattern === pattern} className={`pattern-option pattern-${pattern} ${draft.backgroundType === 'pattern' && draft.pattern === pattern ? 'selected' : ''}`} onClick={() => setDraft({ ...draft, backgroundType: 'pattern', pattern, backgroundUrl: '', backgroundName: '' })}><span>{['点点', '方格', '波纹'][i]}</span>{draft.backgroundType === 'pattern' && draft.pattern === pattern && <Check size={15} />}</button>)}</div></section>
        <section className="settings-section"><h3>自定义背景</h3><button className="background-picker" disabled={busy} onClick={() => void run(async () => { const chosen = await api.PickBackground(); if (chosen) setDraft({ ...draft, backgroundType: chosen.backgroundType, backgroundUrl: chosen.backgroundUrl, backgroundName: chosen.backgroundName }) })}><span className="background-picker-icon">{draft.backgroundType === 'video' ? <Video size={22} /> : <ImagePlus size={22} />}</span><span><strong>{draft.backgroundName || '选择本地图片或视频'}</strong><small>PNG、JPG、WebP · MP4、WebM</small></span><Upload size={17} /></button><label className="slider-label" htmlFor="bg-opacity">背景可见度 <span>{Math.round(draft.opacity * 100)}%</span></label><input id="bg-opacity" className="opacity-slider" type="range" min="0.05" max="1" step="0.01" value={draft.opacity} onChange={e => setDraft({ ...draft, opacity: Number(e.target.value) })} /></section>
        <div className="setting-row"><span><strong>界面动效</strong><small>界面过渡与视频背景播放</small></span><button role="switch" aria-checked={draft.motion} aria-label="界面动效" className={`toggle ${draft.motion ? 'on' : ''}`} onClick={() => setDraft({ ...draft, motion: !draft.motion })}><span /></button></div>
        <div className="setting-row"><span><strong>关闭窗口时收起到托盘</strong><small>{draft.closeToTray ? '点击托盘图标可重新打开，右键可退出' : '关闭窗口时直接退出程序'}</small></span><button role="switch" aria-checked={draft.closeToTray} aria-label="关闭窗口时收起到托盘" className={`toggle ${draft.closeToTray ? 'on' : ''}`} onClick={() => setDraft({ ...draft, closeToTray: !draft.closeToTray })}><span /></button></div>
        {error && <p role="alert" className="form-error">{error}</p>}<div className="modal-footer"><button className="text-button" onClick={() => setDraft(defaultSettings)}>恢复默认</button><button className="btn btn-primary" disabled={busy} onClick={() => void run(async () => { setSettings(await api.SaveSettings(draft)); completeModal(); notify('设置已保存') })}>{busy ? '保存中…' : '保存设置'}<Check size={16} /></button></div>
      </> : modal === 'help' ? <><span className="modal-emblem"><Fingerprint size={28} /></span><h2 id="dialog-title">使用帮助</h2><div className="help-list"><div><Clipboard size={20} /><span><strong>导入令牌</strong><p>复制二维码图片或令牌链接，在主界面按 Ctrl + V，确认账户后即可导入。</p></span></div><div><QrCode size={20} /><span><strong>从 Google 验证器迁移</strong><p>在手机验证器中选择“转移账号 → 导出账号”。将导出的二维码图片传到电脑，依次粘贴或选择图片；全部读取后确认导入。</p></span></div><div><KeyRound size={20} /><span><strong>复制验证码</strong><p>点击验证码即可复制。倒计时结束后自动刷新，请保持电脑时间准确。</p></span></div><div><LockKeyhole size={20} /><span><strong>密钥留在本机</strong><p>Windows 版本使用当前系统用户的 DPAPI 加密保存密钥。卸载或迁移前，请妥善保留各服务的恢复码。</p></span></div><div><Palette size={20} /><span><strong>自定义背景</strong><p>选择本地图片或视频，调整可见度。关闭界面动效时，视频背景也会暂停。</p></span></div></div><button className="btn btn-primary full-width" onClick={closeModal}>关闭</button></> : <>
        <h2 id="dialog-title">{editing ? '编辑令牌' : '添加令牌'}</h2>
        {!editing && <div className={`import-tabs ${modal === 'manual' ? 'manual-active' : ''}`} role="tablist" aria-label="令牌添加方式" onKeyDown={e => { if (busy || closing || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) return; e.preventDefault(); const next = e.key === 'Home' ? 'import' : e.key === 'End' ? 'manual' : modal === 'import' ? 'manual' : 'import'; switchTokenMode(next); e.currentTarget.querySelector<HTMLButtonElement>(`#${next}-tab`)?.focus() }}><span className="import-tab-indicator" aria-hidden="true" /><button id="import-tab" role="tab" aria-selected={modal === 'import'} aria-controls="import-panel" tabIndex={modal === 'import' ? 0 : -1} className={modal === 'import' ? 'active' : ''} disabled={busy} onClick={() => switchTokenMode('import')}><QrCode size={16} />二维码 / 链接</button><button id="manual-tab" role="tab" aria-selected={modal === 'manual'} aria-controls="manual-panel" tabIndex={modal === 'manual' ? 0 : -1} className={modal === 'manual' ? 'active' : ''} disabled={busy} onClick={() => switchTokenMode('manual')}><KeyRound size={16} />手动输入</button></div>}
        <AnimatedPanel mode={modal === 'import' ? 'import' : 'manual'} motion={dialogMotion} labelledBy={editing ? 'dialog-title' : undefined}>
        {modal === 'import' ? <ImportPanel previews={previews} uri={uri} busy={busy} error={error} onUri={setUri} onPaste={() => void pasteClipboard()} onImages={files => void readImages(files)} onReadText={() => void readImportText()} onReset={() => { setPreviews([]); setUri(''); setError('') }} onImport={() => void importTokens()} /> : <form onSubmit={e => { e.preventDefault(); void run(async () => { if (editing) await api.UpdateToken(editing.id, input); else await api.AddToken(input); await reload(); completeModal(); setFilter('all'); setQuery(''); notify(editing ? '令牌已更新' : '令牌已添加') }) }}><div className="form-row"><label>服务名称<input required maxLength={160} autoComplete="off" value={input.issuer} onChange={e => setInput({ ...input, issuer: e.target.value })} placeholder="例如 Google、GitHub" /></label><label>账户<input required maxLength={256} autoComplete="off" value={input.account} onChange={e => setInput({ ...input, account: e.target.value })} placeholder="邮箱或用户名" /></label></div><label className="form-label">{editing ? '更换密钥' : '验证密钥'}<span className="optional-label">{editing ? '选填，留空保留原密钥' : 'Base32'}</span><input className="secret-input" type="password" required={!editing} autoComplete="new-password" spellCheck={false} value={input.secret} onChange={e => setInput({ ...input, secret: e.target.value })} placeholder={editing ? '留空，保持原密钥' : '粘贴服务提供的验证密钥'} /></label><div className="form-row"><label>分组 <span className="optional-label">选填</span><input list="token-groups" value={input.group} maxLength={80} onChange={e => setInput({ ...input, group: e.target.value })} placeholder="例如 工作、个人" /><datalist id="token-groups">{groups.map(g => <option key={g} value={g} />)}</datalist></label><label>点缀色<span className="color-choices">{colors.map(color => <button key={color} type="button" aria-label={`选择颜色 ${color}`} aria-pressed={input.color === color} style={{ background: color }} onClick={() => setInput({ ...input, color })}>{input.color === color && <Check size={13} />}</button>)}</span></label></div><button type="button" className={`advanced-button ${advanced ? 'expanded' : ''}`} onClick={() => setAdvanced(!advanced)}><Settings2 size={14} />高级选项<ChevronDown size={15} /></button>{advanced && <div className="advanced-fields"><label>算法<select aria-label="算法" value={input.algorithm} onChange={e => setInput({ ...input, algorithm: e.target.value })}><option>SHA1</option><option>SHA256</option><option>SHA512</option></select></label><label>位数<select aria-label="位数" value={input.digits} onChange={e => setInput({ ...input, digits: Number(e.target.value) })}><option value={6}>6 位</option><option value={8}>8 位</option></select></label><label>刷新周期（秒）<input aria-label="刷新周期（秒）" type="number" min={1} max={120} value={input.period} onChange={e => setInput({ ...input, period: Number(e.target.value) })} /></label></div>}{error && <p role="alert" className="form-error">{error}</p>}<div className="modal-footer"><button type="submit" className="btn btn-primary" disabled={busy}>{busy ? '保存中…' : editing ? '保存更改' : '添加令牌'}<Check size={16} /></button></div></form>}
        </AnimatedPanel>
      </>}
    </div></div>}
    {toast && <div className="toast" role="status"><span><Check size={15} /></span>{toast}</div>}
  </div></IconTheme.Provider>
}
export default App
