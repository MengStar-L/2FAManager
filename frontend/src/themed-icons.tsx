import { createContext, useContext, type ReactNode } from 'react'
import * as Lucide from 'lucide-react'
import type { LucideIcon, LucideProps } from 'lucide-react'

export const IconTheme = createContext<'regular' | 'anime'>('regular')

// Rounded, hand-drawn counterparts keep each action recognizable in the cute theme.
const cutePaths: Partial<Record<string, ReactNode>> = {
  Search: <><circle cx="10.5" cy="10.5" r="6.7" /><path d="m16 16 4.7 4.7M7.5 9a3.4 3.4 0 0 1 2-1.7" /></>,
  Clipboard: <><path d="M8 5H6a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2" /><rect x="8" y="2" width="8" height="5" rx="2.5" /><path d="M9 12v1m6-1v1m-5.5 3a3.5 3.5 0 0 0 5 0" /></>,
  Plus: <path d="M10 4c0-1 4-1 4 0v6h6c1 0 1 4 0 4h-6v6c0 1-4 1-4 0v-6H4c-1 0-1-4 0-4h6Z" />,
  LayoutGrid: <><rect x="3" y="3" width="7" height="7" rx="2.8" /><rect x="14" y="3" width="7" height="7" rx="2.8" /><rect x="3" y="14" width="7" height="7" rx="2.8" /><path d="m17.5 13 1.3 3.2 3.2 1.3-3.2 1.3-1.3 3.2-1.3-3.2-3.2-1.3 3.2-1.3Z" /></>,
  Star: <path d="M11 3.4a1.1 1.1 0 0 1 2 0l2.2 4.4 4.9.8a1.1 1.1 0 0 1 .6 1.9l-3.5 3.4.8 4.9a1.1 1.1 0 0 1-1.6 1.1L12 17.6l-4.4 2.3A1.1 1.1 0 0 1 6 18.8l.8-4.9-3.5-3.4a1.1 1.1 0 0 1 .6-1.9l4.9-.8Z" />,
  Palette: <><path d="M12 3a9 9 0 1 0 0 18h1a2 2 0 0 0 1-3.7 1.7 1.7 0 0 1 1-3.1h2A4 4 0 0 0 21 10c0-4-4.2-7-9-7Z" /><circle cx="7" cy="11" r=".7" fill="currentColor" /><circle cx="10" cy="7" r=".7" fill="currentColor" /><circle cx="15" cy="7.5" r=".7" fill="currentColor" /><path d="m7.5 15 1 .8 1-.8" /></>,
  CircleHelp: <><path d="M20.5 11.5c0 5-3.4 8.5-8.5 8.5h-3l-4 2v-4C3.5 16.5 3 14.1 3 11.5 3 6.5 6.6 3 12 3s8.5 3.5 8.5 8.5Z" /><path d="M9.5 8.5C9.5 5.5 16 5.5 15 9c-.5 1.7-3 1.7-3 3.5m0 3h.01" /></>,
  KeyRound: <><path d="M13.8 10.2a5.8 5.8 0 1 0-4 4L7 17v3H4v2H1v-4l7.1-7.1" /><path d="m15.5 3 .6 1.5 1.5.6-1.5.6-.6 1.5-.6-1.5-1.5-.6 1.5-.6Z" /></>,
  ShieldCheck: <><path d="M12 2.8c2.1 1.8 5.3 2.8 8 3.4v6c0 4.8-4 7.7-8 9-4-1.3-8-4.2-8-9v-6c2.7-.6 5.9-1.6 8-3.4Z" /><path d="m8.5 11.8 2.4 2.5 4.8-5" /></>,
  Copy: <><path d="M7 5V4a2 2 0 0 1 2-2h9a3 3 0 0 1 3 3v9a2 2 0 0 1-2 2h-1" /><rect x="3" y="7" width="13" height="15" rx="3.5" /><path d="M7 13v1m5-1v1m-4 3a2.5 2.5 0 0 0 3 0" /></>,
  Settings2: <><path d="M4 6h3m6 0h7M4 12h9m6 0h1M4 18h2m6 0h8" /><rect x="7" y="3" width="6" height="6" rx="2.5" /><rect x="13" y="9" width="6" height="6" rx="2.5" /><rect x="6" y="15" width="6" height="6" rx="2.5" /></>,
  PanelLeftClose: <><rect x="2.5" y="3.5" width="19" height="17" rx="4" /><path d="M8 4v16m8-11-3 3 3 3" /></>,
  PanelLeftOpen: <><rect x="2.5" y="3.5" width="19" height="17" rx="4" /><path d="M8 4v16m5-11 3 3-3 3" /></>,
}

function themed(name: string, Regular: LucideIcon) {
  return function ThemedIcon({ size = 24, strokeWidth, ...props }: LucideProps) {
    const anime = useContext(IconTheme) === 'anime'
    if (!anime || !cutePaths[name]) return <Regular size={size} strokeWidth={strokeWidth ?? (anime ? 2.15 : 2)} {...props} />
    return <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={strokeWidth ?? 1.9} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" {...props}>{cutePaths[name]}</svg>
  }
}

export const ArrowDownToLine = themed('ArrowDownToLine', Lucide.ArrowDownToLine)
export const ArrowLeft = themed('ArrowLeft', Lucide.ArrowLeft)
export const ArrowRight = themed('ArrowRight', Lucide.ArrowRight)
export const ArrowUpRight = themed('ArrowUpRight', Lucide.ArrowUpRight)
export const RefreshCw = themed('RefreshCw', Lucide.RefreshCw)
export const Check = themed('Check', Lucide.Check)
export const CheckCheck = themed('CheckCheck', Lucide.CheckCheck)
export const ChevronDown = themed('ChevronDown', Lucide.ChevronDown)
export const CircleHelp = themed('CircleHelp', Lucide.CircleHelp)
export const Clipboard = themed('Clipboard', Lucide.Clipboard)
export const Copy = themed('Copy', Lucide.Copy)
export const Ellipsis = themed('Ellipsis', Lucide.Ellipsis)
export const Fingerprint = themed('Fingerprint', Lucide.Fingerprint)
export const ImagePlus = themed('ImagePlus', Lucide.ImagePlus)
export const KeyRound = themed('KeyRound', Lucide.KeyRound)
export const LayoutGrid = themed('LayoutGrid', Lucide.LayoutGrid)
export const LockKeyhole = themed('LockKeyhole', Lucide.LockKeyhole)
export const Maximize2 = themed('Maximize2', Lucide.Maximize2)
export const Minus = themed('Minus', Lucide.Minus)
export const Palette = themed('Palette', Lucide.Palette)
export const PanelLeftClose = themed('PanelLeftClose', Lucide.PanelLeftClose)
export const PanelLeftOpen = themed('PanelLeftOpen', Lucide.PanelLeftOpen)
export const Plus = themed('Plus', Lucide.Plus)
export const QrCode = themed('QrCode', Lucide.QrCode)
export const Search = themed('Search', Lucide.Search)
export const Settings2 = themed('Settings2', Lucide.Settings2)
export const ShieldCheck = themed('ShieldCheck', Lucide.ShieldCheck)
export const Star = themed('Star', Lucide.Star)
export const Trash2 = themed('Trash2', Lucide.Trash2)
export const Upload = themed('Upload', Lucide.Upload)
export const Video = themed('Video', Lucide.Video)
export const X = themed('X', Lucide.X)
