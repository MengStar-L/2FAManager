import type { CSSProperties } from 'react'

type RGB = [number, number, number]
export type HSV = { h: number; s: number; v: number }
const cream: RGB = [255, 252, 247]
export const accentPresets = [
  { name: '紫色', color: '#8773b7' }, { name: '粉色', color: '#c77d9e' },
  { name: '蓝色', color: '#4f8fc8' }, { name: '青色', color: '#319b9d' },
  { name: '绿色', color: '#6b9561' }, { name: '橙色', color: '#d18a43' },
  { name: '红色', color: '#bf646e' }, { name: '灰色', color: '#7b818b' },
]

export function normalizeHex(value: string): string | null {
  const hex = value.trim().replace(/^#/, '')
  if (/^[\da-f]{6}$/i.test(hex)) return '#' + hex.toLowerCase()
  if (/^[\da-f]{3}$/i.test(hex)) return '#' + [...hex].map(c => c + c).join('').toLowerCase()
  return null
}

function rgb(hex: string): RGB { return [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16)) as RGB }
function hex(rgb: RGB) { return '#' + rgb.map(v => Math.round(v).toString(16).padStart(2, '0')).join('') }
function mix(a: RGB, b: RGB, fraction: number): RGB { return a.map((c, i) => Math.round(c + (b[i] - c) * fraction)) as RGB }
function luminance(rgb: RGB) {
  const linear = rgb.map(v => { const s = v / 255; return s <= .04045 ? s / 12.92 : ((s + .055) / 1.055) ** 2.4 })
  return .2126 * linear[0] + .7152 * linear[1] + .0722 * linear[2]
}
function contrast(a: RGB, b: RGB) { const x = luminance(a); const y = luminance(b); return (Math.max(x, y) + .05) / (Math.min(x, y) + .05) }
function readable(color: RGB, surfaces: RGB[], minimum: number): RGB {
  for (let step = 0; step <= 100; step++) {
    const candidate = mix(color, [0, 0, 0], step / 100)
    if (surfaces.every(surface => contrast(candidate, surface) >= minimum)) return candidate
  }
  return [0, 0, 0]
}

export function accentForeground(color: string) {
  const selected = rgb(normalizeHex(color) || '#8773b7')
  const ivory: RGB = [255, 250, 244]
  // Keep medium and deep accents soft with ivory. Light colours use a tinted
  // charcoal instead of switching every control to stark, pure black.
  if (contrast(selected, ivory) >= 3) return hex(ivory)
  return hex(readable(mix(selected, [55, 48, 65], .9), [selected], 4.5))
}

export function accentStyle(color: string): CSSProperties {
  const selected = rgb(normalizeHex(color) || '#8773b7')
  const soft = mix(cream, selected, .12)
  const tint = mix(cream, selected, .045)
  const surfaces = [cream, soft, tint, [251, 248, 241] as RGB]
  const text = readable(selected, surfaces, 4.8)
  const ink = readable(selected, surfaces, 7)
  const icon = readable(selected, surfaces, 3.3)
  return {
    '--ui-accent': hex(selected), '--ui-rgb': selected.join(' '),
    '--on-accent': accentForeground(hex(selected)), '--ui-text': hex(text),
    '--ui-ink': hex(ink), '--ui-muted': hex(text), '--ui-icon': hex(icon),
    '--ui-focus': hex(icon), '--ui-soft': hex(soft), '--ui-tint': hex(tint),
    '--ui-border': hex(mix(cream, selected, .23)), '--ui-shadow-rgb': ink.join(' '), '--ui-surface-rgb': tint.join(' '),
    '--ui-glow': hex(mix(cream, selected, .27)),
  } as CSSProperties
}

export function hexToHSV(value: string, previousHue = 0): HSV {
  const [r, g, b] = rgb(normalizeHex(value) || '#8773b7').map(v => v / 255)
  const max = Math.max(r, g, b); const min = Math.min(r, g, b); const difference = max - min
  let h = previousHue
  if (difference) { h = max === r ? (g - b) / difference + (g < b ? 6 : 0) : max === g ? (b - r) / difference + 2 : (r - g) / difference + 4; h *= 60 }
  return { h, s: max ? difference / max * 100 : 0, v: max * 100 }
}

export function hsvToHex({ h, s, v }: HSV): string {
  const saturation = Math.max(0, Math.min(s, 100)) / 100
  const value = Math.max(0, Math.min(v, 100)) / 100
  const hue = (h % 360 + 360) % 360 / 60
  const chroma = value * saturation; const x = chroma * (1 - Math.abs(hue % 2 - 1)); const m = value - chroma
  const channels = hue < 1 ? [chroma, x, 0] : hue < 2 ? [x, chroma, 0] : hue < 3 ? [0, chroma, x] : hue < 4 ? [0, x, chroma] : hue < 5 ? [x, 0, chroma] : [chroma, 0, x]
  return hex(channels.map(c => (c + m) * 255) as RGB)
}
