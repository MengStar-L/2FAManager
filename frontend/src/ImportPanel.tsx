import { useRef } from 'react'
import type { ImportPreview } from './api'
import { ArrowDownToLine, ArrowLeft, ArrowRight, Check, Clipboard, QrCode, Upload } from './themed-icons'
import { ProviderIcon } from './ProviderIcon'
import { migrationProgress } from './importCollection'
import './import-panel.css'

type Props = {
  previews: ImportPreview[]; uri: string; busy: boolean; error: string
  onUri(value: string): void; onPaste(): void; onImages(files: File[]): void
  onReadText(): void; onReset(): void; onImport(): void
}

export function ImportPanel({ previews, uri, busy, error, onUri, onPaste, onImages, onReadText, onReset, onImport }: Props) {
  const fileRef = useRef<HTMLInputElement>(null)
  const batches = migrationProgress(previews)
  const complete = batches.every(batch => batch.complete)
  const populated = previews.length > 0
  const linkFields = <>
    <textarea className="uri-input" aria-label="令牌链接" placeholder="otpauth://totp/… 或 otpauth-migration://…" value={uri} onChange={event => onUri(event.target.value)} maxLength={256 * 1024} rows={2} spellCheck={false} disabled={busy} />
    {!populated && <p className="field-hint">支持 TOTP 和 Google 验证器导出二维码，多张可连续读取。</p>}
    {!populated && error && <p role="alert" className="form-error">{error}</p>}
    <div className={populated ? 'import-link-action' : 'modal-footer'}><button className={populated ? 'btn btn-secondary' : 'btn btn-primary'} disabled={busy || !uri.trim()} onClick={onReadText}>{busy ? '识别中…' : '识别链接'}<ArrowRight size={16} /></button></div>
  </>
  return <div className="import-flow" onDragOver={event => event.preventDefault()} onDrop={event => { event.preventDefault(); if (!busy) onImages(Array.from(event.dataTransfer.files)) }}>
    <input ref={fileRef} hidden type="file" aria-label="二维码图片" multiple accept="image/png,image/jpeg,image/webp,image/gif,image/bmp" disabled={busy} onChange={event => { const files = Array.from(event.target.files || []); event.target.value = ''; onImages(files) }} />
    {populated ? <>
      <div className={`preview-success ${complete ? '' : 'preview-pending'}`}><span>{complete ? <Check size={20} /> : <QrCode size={20} />}</span><div><strong>找到 {previews.length} 枚令牌</strong><p>{complete ? '确认账户后导入。' : '请继续读取剩余二维码。'}</p></div></div>
      {batches.length > 0 && <div className="migration-progress" role="status" aria-live="polite">{batches.map((batch, index) => <div key={batch.id}><span>{batches.length > 1 ? `第 ${index + 1} 批 · ` : ''}已读取 {batch.pages}/{batch.size} 张二维码</span><span className="migration-progress-track" aria-hidden="true"><i style={{ width: `${batch.pages / batch.size * 100}%` }} /></span></div>)}</div>}
      <div className="import-preview-list">{previews.map((preview, index) => <div className="import-preview" key={index}><span className="preview-provider"><ProviderIcon issuer={preview.issuer} account={preview.account} /></span><div><strong>{preview.issuer || '未命名令牌'}</strong><small>{preview.account || '个人账户'}</small></div><Check size={17} /></div>)}</div>
      <div className="import-continue"><button className="btn btn-secondary" disabled={busy} onClick={onPaste}><Clipboard size={15} />{busy ? '正在识别…' : '继续粘贴'}</button><button className="btn btn-secondary" disabled={busy} onClick={() => fileRef.current?.click()}><Upload size={15} />添加图片</button></div>
      <details className="import-more-links"><summary tabIndex={0}>添加链接</summary>{linkFields}</details>
      {error && <p role="alert" className="form-error">{error}</p>}
      <div className="modal-footer import-confirm"><button className="btn btn-secondary" onClick={onReset} disabled={busy}><ArrowLeft size={15} />重新选择</button><button className="btn btn-primary" disabled={busy || !complete} onClick={onImport}>{busy ? '处理中…' : '确认导入'}<ArrowDownToLine size={16} /></button></div>
    </> : <>
      <div className="qr-drop-area"><span className="scan-symbol"><QrCode size={38} strokeWidth={1.5} /><i /><i /><i /><i /></span><button className="btn btn-primary" disabled={busy} onClick={onPaste}><Clipboard size={16} />{busy ? '正在识别…' : '读取剪贴板'}</button><button className="text-button" disabled={busy} onClick={() => fileRef.current?.click()}><Upload size={14} />或选择 / 拖入二维码图片</button></div>
      <div className="or-divider"><span />或粘贴令牌链接<span /></div>
      {linkFields}
    </>}
  </div>
}
