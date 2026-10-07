import icons from './assets/brands/icons.json'

type Brand = keyof typeof icons
const aliases: Record<string, Brand> = {
  openai: 'openai', 'open ai': 'openai', 'open-ai': 'openai', open_ai: 'openai',
  'openai.com': 'openai', 'www.openai.com': 'openai', chatgpt: 'openai', 'chatgpt.com': 'openai',
  google: 'google', 'google.com': 'google', gmail: 'google', 'google workspace': 'google',
  microsoft: 'microsoft', 'microsoft.com': 'microsoft', 'microsoft account': 'microsoft', outlook: 'microsoft', 'outlook.com': 'microsoft', 'office 365': 'microsoft', 'microsoft 365': 'microsoft',
  github: 'github', 'github.com': 'github',
  apple: 'apple', 'apple id': 'apple', 'apple account': 'apple', 'apple.com': 'apple', icloud: 'apple',
  amazon: 'amazon', 'amazon.com': 'amazon',
  discord: 'discord', 'discord.com': 'discord',
  telegram: 'telegram', 'telegram.org': 'telegram',
  dropbox: 'dropbox', 'dropbox.com': 'dropbox',
  notion: 'notion', 'notion.so': 'notion', 'notion.com': 'notion',
  figma: 'figma', 'figma.com': 'figma',
}

// The real brand shapes are bundled locally; issuer/account text never becomes a URL.
export function ProviderIcon({ issuer, account = '' }: { issuer: string; account?: string }) {
  const name = issuer.trim().toLowerCase()
  const brand = Object.hasOwn(aliases, name) ? aliases[name] : undefined
  if (!brand) return <span className="provider-initials" aria-hidden="true">{Array.from((issuer || account || '2F').trim()).slice(0, 2).join('').toUpperCase()}</span>
  return <svg className={`provider-glyph brand-${brand}`} data-brand={brand} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" focusable="false"><path d={icons[brand].path} /></svg>
}
