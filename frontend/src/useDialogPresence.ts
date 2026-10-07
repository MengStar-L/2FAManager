import { useCallback, useEffect, useRef, useState } from 'react'

export const dialogExitDuration = 180

// Keep the current dialog and its data mounted until its exit animation ends.
export function useDialogPresence(motion: boolean, onExited: () => void) {
  const [closing, setClosing] = useState(false)
  const closingRef = useRef(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const onExitedRef = useRef(onExited)
  const motionRef = useRef(motion)
  onExitedRef.current = onExited
  motionRef.current = motion

  const cancel = useCallback(() => {
    if (timer.current) clearTimeout(timer.current)
    timer.current = null
    closingRef.current = false
    setClosing(false)
  }, [])

  const finish = useCallback(() => {
    if (!closingRef.current) return
    cancel()
    onExitedRef.current()
  }, [cancel])

  const close = useCallback((beforeExit?: () => void) => {
    if (closingRef.current) return
    closingRef.current = true
    if (!motionRef.current) {
      finish()
      return
    }
    beforeExit?.()
    setClosing(true)
    // Animation end is authoritative; this covers interrupted WebView paints.
    timer.current = setTimeout(finish, dialogExitDuration + 240)
  }, [finish])

  useEffect(() => { if (!motion && closing) finish() }, [motion, closing, finish])
  useEffect(() => () => { if (timer.current) clearTimeout(timer.current) }, [])

  return { closing, close, cancel, finish }
}
