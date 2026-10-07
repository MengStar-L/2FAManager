import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import { Check } from './themed-icons'
import { accentForeground, accentPresets, hexToHSV, hsvToHex, normalizeHex, type HSV } from './colorTheme'

type Props = { value: string; onChange: (color: string) => void }

export function AccentPicker({ value, onChange }: Props) {
  const [hsv, setHSV] = useState(() => hexToHSV(value))
  const [input, setInput] = useState(value.toUpperCase())
  const ownChange = useRef('')
  useEffect(() => {
    if (value === ownChange.current) return
    setInput(value.toUpperCase())
    setHSV(current => hexToHSV(value, current.h))
  }, [value])
  const emit = (color: string) => { ownChange.current = color; onChange(color) }
  const choose = (color: string) => { setHSV(current => hexToHSV(color, current.h)); setInput(color.toUpperCase()); emit(color) }
  const update = (next: HSV) => { setHSV(next); const color = hsvToHex(next); setInput(color.toUpperCase()); emit(color) }
  const point = (e: PointerEvent<HTMLDivElement>) => {
    const rect = e.currentTarget.getBoundingClientRect()
    update({ ...hsv, s: Math.max(0, Math.min(100, (e.clientX - rect.left) / rect.width * 100)), v: Math.max(0, Math.min(100, (1 - (e.clientY - rect.top) / rect.height) * 100)) })
  }
  const keyboard = (e: KeyboardEvent<HTMLDivElement>) => {
    const step = e.shiftKey ? 10 : 1
    const next = { ...hsv }
    if (e.key === 'ArrowLeft') next.s -= step
    else if (e.key === 'ArrowRight') next.s += step
    else if (e.key === 'ArrowUp') next.v += step
    else if (e.key === 'ArrowDown') next.v -= step
    else if (e.key === 'Home') next.s = 0
    else if (e.key === 'End') next.s = 100
    else return
    e.preventDefault()
    update({ ...next, s: Math.max(0, Math.min(100, next.s)), v: Math.max(0, Math.min(100, next.v)) })
  }
  const valid = normalizeHex(input)
  return <section className="settings-section accent-section"><h3>点缀色</h3>
    <div className="accent-presets" aria-label="预设点缀色">{accentPresets.map(preset => <button key={preset.color} type="button" className="accent-swatch" aria-label={`点缀色 ${preset.name}`} title={preset.name} aria-pressed={value.toLowerCase() === preset.color} style={{ background: preset.color, color: accentForeground(preset.color) }} onClick={() => choose(preset.color)}>{value.toLowerCase() === preset.color && <Check size={15} />}</button>)}</div>
    <div className="accent-plane" role="slider" tabIndex={0} aria-label="颜色饱和度与亮度" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(hsv.s)} aria-valuetext={`饱和度 ${Math.round(hsv.s)}%，亮度 ${Math.round(hsv.v)}%`} aria-describedby="accent-keyboard-hint" style={{ backgroundColor: `hsl(${hsv.h} 100% 50%)` }} onKeyDown={keyboard} onPointerDown={e => { e.currentTarget.focus(); e.currentTarget.setPointerCapture(e.pointerId); point(e) }} onPointerMove={e => { if (e.currentTarget.hasPointerCapture(e.pointerId)) point(e) }} onPointerUp={e => { if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId) }}><span className="accent-cursor" style={{ left: `${hsv.s}%`, top: `${100 - hsv.v}%`, background: value }} /></div>
    <span className="sr-only" id="accent-keyboard-hint">左右方向键调整饱和度，上下方向键调整亮度，按住 Shift 每次调整 10%。</span>
    <input className="accent-hue" type="range" min="0" max="360" step="1" aria-label="色相" value={hsv.h} onChange={e => update({ ...hsv, h: Number(e.target.value) })} style={{ '--picker-hue': `hsl(${hsv.h} 100% 50%)` } as React.CSSProperties} />
    <div className="accent-custom-row"><span className="accent-current" style={{ background: value, color: accentForeground(value) }} aria-hidden="true"><Check size={17} /></span><label className="accent-hex-label">HEX<input aria-label="自定义颜色 HEX" spellCheck={false} maxLength={7} value={input} aria-invalid={!valid} onChange={e => { const next = e.target.value; setInput(next); const color = normalizeHex(next); if (color) { setHSV(current => hexToHSV(color, current.h)); emit(color) } }} onBlur={() => setInput((valid || value).toUpperCase())} onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); setInput((valid || value).toUpperCase()) } }} /></label></div>
    {!valid && <p className="accent-input-hint" role="status">请输入 3 位或 6 位十六进制颜色，例如 #8773B7。</p>}
  </section>
}
