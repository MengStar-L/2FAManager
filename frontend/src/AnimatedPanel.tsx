import { useLayoutEffect, useRef, useState, type ReactNode } from 'react'

type Props = { mode: 'import' | 'manual'; motion: boolean; children: ReactNode; labelledBy?: string }

export function AnimatedPanel({ mode, motion, children, labelledBy }: Props) {
  const panel = useRef<HTMLDivElement>(null)
  const [height, setHeight] = useState<number>()

  useLayoutEffect(() => {
    const content = panel.current
    if (!content) return
    const measure = () => setHeight(content.offsetHeight)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(content)
    return () => observer.disconnect()
  }, [mode])

  return <div className="import-panel-shell" data-motion={motion ? 'on' : 'off'} style={{ height: motion ? height : undefined }}>
    <div key={mode} ref={panel} className="import-panel" data-mode={mode} role="tabpanel" id={`${mode}-panel`} aria-labelledby={labelledBy || `${mode}-tab`}>
      {children}
    </div>
  </div>
}
