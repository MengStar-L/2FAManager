/** Keep visual focus separate from DOM focus, so switching apps never loses a
 * draft's caret or breaks the dialog's keyboard focus restoration. */
export function trackWindowFocus() {
  const root = document.documentElement
  const setActive = (active: boolean) => { root.dataset.windowActive = String(active && !document.hidden) }
  const onFocus = () => setActive(true)
  const onBlur = () => setActive(false)
  const onVisibility = () => setActive(document.hasFocus())
  const onPointer = () => { root.dataset.inputMethod = 'pointer'; onFocus() }
  const onKey = (event: KeyboardEvent) => {
    if (['Alt', 'Control', 'Meta', 'Shift'].includes(event.key)) return
    root.dataset.inputMethod = 'keyboard'
    onFocus()
  }

  setActive(document.hasFocus())
  root.dataset.inputMethod = 'keyboard'
  window.addEventListener('focus', onFocus)
  window.addEventListener('blur', onBlur)
  document.addEventListener('visibilitychange', onVisibility)
  document.addEventListener('pointerdown', onPointer, true)
  document.addEventListener('keydown', onKey, true)
  return () => {
    window.removeEventListener('focus', onFocus)
    window.removeEventListener('blur', onBlur)
    document.removeEventListener('visibilitychange', onVisibility)
    document.removeEventListener('pointerdown', onPointer, true)
    document.removeEventListener('keydown', onKey, true)
    delete root.dataset.windowActive
    delete root.dataset.inputMethod
  }
}
