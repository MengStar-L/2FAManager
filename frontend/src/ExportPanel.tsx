import { useEffect, useRef, useState } from 'react'
import { api, type Token, type TokenExport, type ExportCredential } from './api'
import { ArrowDownToLine, Check, Copy, KeyRound, LockKeyhole, Search, ShieldCheck, Trash2 } from './themed-icons'
import { ProviderIcon } from './ProviderIcon'
import './export-panel.css'

type Exported = { exportId: string; accounts: Pick<ExportCredential, 'id' | 'issuer' | 'account'>[] }
type Props = { tokens: Token[]; initialSelection: string[]; closing: boolean; onBusy: (busy: boolean) => void; onClose: () => void; onChanged: () => Promise<void>; notify: (text: string) => void }
const message = (error: unknown) => error instanceof Error ? error.message : String(error)

export function ExportPanel({ tokens, initialSelection, closing, onBusy, onClose, onChanged, notify }: Props) {
  const [selected, setSelected] = useState<string[]>(initialSelection)
  const [query, setQuery] = useState('')
  const [preview, setPreview] = useState<TokenExport | null>(null)
  const [exported, setExported] = useState<Exported | null>(null)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const pending = useRef(false)
  const activeExport = useRef('')
  const alive = useRef(true)
  const panelRef = useRef<HTMLDivElement>(null)
  const resultFocus = useRef<HTMLHeadingElement>(null)
  const discard = () => { const id = activeExport.current; activeExport.current = ''; if (id) void api.DiscardTokenExport(id).catch(() => {}) }
  useEffect(() => { alive.current = true; return () => { alive.current = false; const id = activeExport.current; activeExport.current = ''; if (id) void api.DiscardTokenExport(id).catch(() => {}) } }, [])
  useEffect(() => { if (closing) { setPreview(null); discard() } }, [closing])
  useEffect(() => { if (exported) resultFocus.current?.focus() }, [exported, confirmDelete])
  useEffect(() => { panelRef.current?.closest('.modal')?.scrollTo({ top: 0 }) }, [Boolean(preview), Boolean(exported), confirmDelete])
  const perform = async (action: () => Promise<void>) => {
    if (pending.current || closing) return
    pending.current = true; setBusy(true); onBusy(true); setError('')
    try { await action() } catch (failure) { if (alive.current) setError(message(failure)) }
    finally { pending.current = false; if (alive.current) setBusy(false); onBusy(false) }
  }
  const choose = (ids: string[]) => { if (pending.current || closing) return; discard(); setPreview(null); setError(''); setSelected(ids) }
  const visible = tokens.filter(token => `${token.issuer} ${token.account} ${token.group}`.toLowerCase().includes(query.toLowerCase()))
  const visibleIds = visible.map(token => token.id)
  const allVisible = visible.length > 0 && visibleIds.every(id => selected.includes(id))
  const validSelected = selected.filter(id => tokens.some(token => token.id === id))
  const view = () => perform(async () => {
    if (!validSelected.length || validSelected.length > 100) return
    discard(); setPreview(null)
    const next = await api.PreviewTokenExport(validSelected)
    if (!next.exportId || !next.credentials.length) throw new Error('没有可查看的账号凭证，请重新选择。')
    if (!alive.current) { void api.DiscardTokenExport(next.exportId).catch(() => {}); return }
    activeExport.current = next.exportId; setPreview(next)
  })
  const finishExport = (bundle: TokenExport) => {
    if (!alive.current) return
    setPreview(null)
    setExported({ exportId: bundle.exportId, accounts: bundle.credentials.map(({ id, issuer, account }) => ({ id, issuer, account })) })
    setConfirmDelete(false)
  }
  const copy = () => perform(async () => { if (!preview) return; await api.CopyTokenExport(preview.exportId); finishExport(preview) })
  const save = () => perform(async () => { if (!preview) return; if (await api.SaveTokenExport(preview.exportId)) finishExport(preview) })
  const keep = () => { if (pending.current) return; discard(); setPreview(null); notify('已保留导出的账号'); onClose() }
  const remove = () => perform(async () => {
    if (!exported) return
    await api.DeleteExportedTokens(exported.exportId)
    activeExport.current = ''
    notify('已删除刚才导出的账号')
    onClose()
    await onChanged()
  })

  if (exported) return <div className="export-panel export-result" ref={panelRef}>
    <span className={`modal-emblem ${confirmDelete ? 'danger-emblem' : ''}`}>{confirmDelete ? <Trash2 size={26} /> : <ShieldCheck size={26} />}</span>
    <h2 id="dialog-title" tabIndex={-1} ref={resultFocus}>{confirmDelete ? '删除已导出的账号？' : '导出完成'}</h2>
    <p className="modal-description">{confirmDelete ? `将从本机删除以下 ${exported.accounts.length} 个账号。请先确认导出的凭证已妥善保存，可以用于恢复。` : `已导出以下 ${exported.accounts.length} 个账号。要保留它们，还是从本机删除？`}</p>
    <div className="exported-accounts" aria-label="刚才导出的账号">{exported.accounts.map(account => <div key={account.id}><ProviderIcon issuer={account.issuer} account={account.account} /><span><strong>{account.issuer || '未命名令牌'}</strong><small>{account.account}</small></span><Check size={14} /></div>)}</div>
    <p className="field-hint">这里只处理刚才导出的账号，其他账号不受影响。关闭窗口会保留账号。</p>
    {error && <p role="alert" className="form-error">{error}</p>}
    <div className="modal-footer export-choice-footer">{confirmDelete ? <><button className="btn btn-secondary" disabled={busy} onClick={() => { setConfirmDelete(false); setError('') }}>返回</button><button className="btn btn-danger" disabled={busy} onClick={() => void remove()}>{busy ? '正在删除…' : `确认删除 ${exported.accounts.length} 个账号`}</button></> : <><button className="text-button danger-text" disabled={busy} onClick={() => setConfirmDelete(true)}><Trash2 size={15} />删除已导出账号</button><button className="btn btn-primary" disabled={busy} onClick={keep}><Check size={15} />保留账号</button></>}</div>
  </div>

  return <div className="export-panel" ref={panelRef}>
    <h2 id="dialog-title">查看与导出凭证</h2>
    <p className="modal-description">选择一个或多个账号，查看密钥并导出。每次最多 100 个账号。</p>
    {preview ? <div className="credential-selection-summary"><span>已选 {preview.credentials.length} 个账号</span><button className="text-button" disabled={busy} onClick={() => choose(selected)}>更改选择</button></div> : <div className="credential-selector">
      <label className="credential-search"><Search size={16} /><input aria-label="搜索待导出账号" placeholder="搜索服务、账号或分组…" value={query} disabled={busy} onChange={event => setQuery(event.target.value)} /></label>
      <div className="credential-select-tools"><span aria-live="polite">已选 {validSelected.length} 个账号</span><button className="text-button" disabled={busy || !visible.length} onClick={() => choose(allVisible ? selected.filter(id => !visibleIds.includes(id)) : [...new Set([...selected, ...visibleIds])])}>{allVisible ? '取消全选' : query ? '全选筛选结果' : '全选'}</button>{selected.length > 0 && <button className="text-button" disabled={busy} onClick={() => choose([])}>清空选择</button>}</div>
      <div className="credential-list" aria-label="选择导出账号">{visible.length ? visible.map(token => <label className={`credential-option ${selected.includes(token.id) ? 'selected' : ''}`} key={token.id}><input type="checkbox" aria-label={`选择 ${token.issuer} ${token.account}`} checked={selected.includes(token.id)} disabled={busy} onChange={() => choose(selected.includes(token.id) ? selected.filter(id => id !== token.id) : [...selected, token.id])} /><ProviderIcon issuer={token.issuer} account={token.account} /><span><strong>{token.issuer || '未命名令牌'}</strong><small>{token.account}</small></span>{token.group && <em>{token.group}</em>}</label>) : <p className="credential-empty">{tokens.length ? '没有匹配的账号' : '暂无可导出的账号'}</p>}</div>
    </div>}
    {validSelected.length > 100 && <p role="alert" className="form-error">每次最多选择 100 个账号，请减少选择。</p>}
    <div className="export-warning"><LockKeyhole size={16} /><span>凭证包含真实密钥，任何拿到它的人都可以生成验证码。请妥善保管。</span></div>
    {preview && <section className="credential-preview" aria-label="所选账号凭证"><div className="credential-keys">{preview.credentials.map(credential => <label key={credential.id}><span><strong>{credential.issuer || '未命名令牌'}</strong> · {credential.account}</span><input aria-label={`${credential.issuer} ${credential.account} 的密钥`} readOnly value={credential.secret} spellCheck={false} autoComplete="off" onFocus={event => event.currentTarget.select()} /></label>)}</div><label className="credential-links-label">完整导出链接<textarea className="export-textarea" aria-label="2FA 密钥导出字符串" readOnly value={preview.text} spellCheck={false} /></label></section>}
    {error && <p role="alert" className="form-error">{error}</p>}
    <div className="modal-footer export-footer">{!preview && <button className="btn btn-secondary" disabled={busy || !validSelected.length || validSelected.length > 100} onClick={() => void view()}><KeyRound size={15} />{busy ? '正在读取…' : '查看凭证'}</button>}<button className="btn btn-primary" disabled={busy || !preview} onClick={() => void copy()}><Copy size={15} />复制字符串</button><button className="btn btn-secondary" disabled={busy || !preview} onClick={() => void save()}><ArrowDownToLine size={15} />保存 TXT</button></div>
  </div>
}
