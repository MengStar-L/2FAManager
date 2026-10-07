import type { Settings, UpdateStatus } from './api'
import { ArrowDownToLine, ArrowUpRight, Check, RefreshCw } from './themed-icons'

type Props = {
  draft: Settings
  changeDraft: (settings: Settings) => void
  status: UpdateStatus
  requesting: boolean
  installing: boolean
  unsaved: boolean
  check: () => Promise<void>
  download: () => Promise<void>
  install: () => Promise<void>
  openRelease: () => Promise<void>
}

const version = (value: string) => value ? `v${value.replace(/^v/i, '')}` : '读取中…'
const size = (bytes: number) => `${(Math.max(0, bytes) / 1024 / 1024).toFixed(1)} MB`

function statusText(status: UpdateStatus) {
  switch (status.phase) {
    case 'checking': return '正在检查更新…'
    case 'available': return `发现新版本 ${version(status.latestVersion)}`
    case 'downloading': return `正在下载 ${version(status.latestVersion)}`
    case 'ready': return `${version(status.latestVersion)} 已下载`
    case 'upToDate': return '已是最新版本'
    case 'error': return '更新暂不可用'
    default: return '尚未检查更新'
  }
}

export function UpdateSettings({ draft, changeDraft, status, requesting, installing, unsaved, check, download, install, openRelease }: Props) {
  const checking = status.phase === 'checking'
  const downloading = status.phase === 'downloading'
  const ready = status.phase === 'ready'
  const progress = Math.max(0, Math.min(100, status.progress || 0))
  const canDownload = status.phase === 'available' || (status.phase === 'error' && status.latestVersion && status.latestVersion !== status.currentVersion)
  const checked = status.lastChecked ? new Date(status.lastChecked) : null
  return <section className="settings-section update-settings" aria-labelledby="update-settings-title">
    <div className="update-heading"><div><h3 id="update-settings-title">软件更新</h3><span className="update-current-version">当前版本 {version(status.currentVersion)}</span></div><button className="btn btn-secondary update-check" disabled={requesting || checking || downloading || installing} onClick={() => void check()}><RefreshCw className={checking ? 'update-spinning' : ''} size={14} />{checking ? '检查中…' : '检查更新'}</button></div>
    <div className={`update-status ${status.phase === 'error' ? 'update-failed' : ''}`} data-phase={status.phase}>
      <span className="update-status-label" role="status">{ready || status.phase === 'upToDate' ? <Check size={14} /> : null}{statusText(status)}</span>
      {downloading && <><div className="update-progress" role="progressbar" aria-label="更新下载进度" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(progress)}><span style={{ width: `${progress}%` }} /></div><div className="update-progress-caption"><span>{size(status.downloadedBytes)}{status.totalBytes > 0 ? ` / ${size(status.totalBytes)}` : ''}</span><span>{Math.round(progress)}%</span></div></>}
      {status.message && <p className="update-message">{status.message}</p>}
      {!checking && !downloading && checked && Number.isFinite(checked.getTime()) && <span className="update-last-checked">上次检查 {checked.toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</span>}
      {(canDownload || ready || status.releaseUrl) && <div className="update-actions">{canDownload && <button className="btn btn-primary" disabled={requesting || installing} onClick={() => void download()}><ArrowDownToLine size={14} />{status.phase === 'error' ? '重试下载' : '下载更新'}</button>}{ready && <button className="btn btn-primary" disabled={requesting || installing || unsaved} onClick={() => void install()}><RefreshCw size={14} />{installing ? '正在重启…' : '安装并重启'}</button>}{status.releaseUrl && <button className="text-button" onClick={() => void openRelease()}>发布说明<ArrowUpRight size={13} /></button>}</div>}
      {ready && unsaved && <p className="update-message">请先保存当前设置，再安装更新。</p>}
    </div>
    <div className="update-option"><span><strong>自动检查更新</strong><small>启动后和运行期间定期检查</small></span><button className={`toggle ${draft.checkUpdatesAutomatically ? 'on' : ''}`} role="switch" aria-label="自动检查更新" aria-checked={draft.checkUpdatesAutomatically} onClick={() => changeDraft({ ...draft, checkUpdatesAutomatically: !draft.checkUpdatesAutomatically, updateAutomatically: draft.checkUpdatesAutomatically ? false : draft.updateAutomatically })}><span /></button></div>
    <div className="update-option"><span><strong>自动更新</strong><small>后台下载，退出时安装，下次启动生效</small></span><button className={`toggle ${draft.updateAutomatically ? 'on' : ''}`} role="switch" aria-label="自动更新" aria-checked={draft.updateAutomatically} onClick={() => changeDraft({ ...draft, updateAutomatically: !draft.updateAutomatically, checkUpdatesAutomatically: draft.updateAutomatically ? draft.checkUpdatesAutomatically : true })}><span /></button></div>
  </section>
}
