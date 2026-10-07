import { useCallback, useEffect, useRef, useState } from 'react'
import { api, defaultUpdateStatus, native, subscribeToUpdates, type UpdateStatus } from './api'

const errorMessage = (error: unknown) => error instanceof Error ? error.message : String(error)
const unavailable = () => new Error('更新服务暂不可用，请重新启动应用。')

export function useUpdates(notify: (message: string) => void, automatically: boolean) {
  const [status, setStatus] = useState<UpdateStatus>(defaultUpdateStatus)
  const [requesting, setRequesting] = useState(false)
  const [installing, setInstalling] = useState(false)
  const requestRef = useRef(false)
  const epoch = useRef(0)
  const notifiedVersion = useRef('')

  const receive = useCallback((next: UpdateStatus) => {
    epoch.current++
    setStatus({ ...defaultUpdateStatus, ...next })
  }, [])

  useEffect(() => {
    let active = true
    const unsubscribe = subscribeToUpdates(next => { if (active) receive(next) })
    const refresh = async () => {
      const requestEpoch = epoch.current
      try {
        if (typeof api.GetUpdateStatus !== 'function') throw unavailable()
        const next = await api.GetUpdateStatus()
        // A response started before a push must not overwrite newer progress.
        if (active && requestEpoch === epoch.current) receive(next)
      } catch (error) {
        if (active && requestEpoch === epoch.current) setStatus(current => ({ ...current, phase: 'error', message: errorMessage(error) }))
      }
    }
    void refresh()
    const timer = setInterval(() => { void refresh() }, 3000)
    return () => { active = false; clearInterval(timer); unsubscribe() }
  }, [receive])

  useEffect(() => {
    if (status.phase !== 'ready' || !status.latestVersion || status.latestVersion === notifiedVersion.current) return
    notifiedVersion.current = status.latestVersion
    notify(automatically ? '更新已下载，退出时安装，下次启动生效。' : '更新已下载，可在设置中安装并重启。')
  }, [status.phase, status.latestVersion, automatically, notify])

  const request = async (operation: () => Promise<UpdateStatus>) => {
    if (requestRef.current) return
    requestRef.current = true
    setRequesting(true)
    const requestEpoch = epoch.current
    try { const next = await operation(); if (requestEpoch === epoch.current) receive(next) }
    catch (error) { if (requestEpoch === epoch.current) setStatus(current => ({ ...current, phase: 'error', message: errorMessage(error) })) }
    finally { requestRef.current = false; setRequesting(false) }
  }
  const check = () => request(() => typeof api.CheckForUpdates === 'function' ? api.CheckForUpdates() : Promise.reject(unavailable()))
  const download = () => request(() => typeof api.DownloadUpdate === 'function' ? api.DownloadUpdate() : Promise.reject(unavailable()))
  const install = async () => {
    if (requestRef.current) return
    requestRef.current = true
    setInstalling(true)
    try { if (typeof api.InstallUpdate !== 'function') throw unavailable(); await api.InstallUpdate(); if (!native) setInstalling(false) }
    catch (error) { setInstalling(false); notify(`安装更新失败：${errorMessage(error)}`) }
    finally { requestRef.current = false }
  }
  const openRelease = async () => { try { if (typeof api.OpenReleasePage !== 'function') throw unavailable(); await api.OpenReleasePage() } catch (error) { notify(errorMessage(error)) } }
  return { status, requesting, installing, check, download, install, openRelease }
}
