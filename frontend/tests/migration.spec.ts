import { expect, test, type Page } from '@playwright/test'
import { mergeImportPreviews, migrationProgress } from '../src/importCollection'
import type { ImportPreview } from '../src/api'

const pageURI = (page: number) => `otpauth-migration://offline?data=fixture-page-${page}`
const pageEntries = (index: number, accounts: string[], overrides: Partial<NonNullable<ImportPreview['migration']>> = {}): ImportPreview[] => accounts.map(account => ({ issuer: 'Google', account, uri: pageURI(index), migration: { id: 'synthetic-export', size: 2, index, ...overrides } }))
const first = pageEntries(0, ['alice@example.test', 'bob@example.test'])
const second = pageEntries(1, ['carol@example.test'])

async function installBridge(page: Page, theme: 'regular' | 'anime' = 'regular') {
  await page.addInitScript(({ first, second, theme }) => {
    const qa = window as typeof window & {
      __scanQueue: string[]; __scanCalls: number; __imageCalls: string[]; __imports: string[][]; __scanDelay: number; go: unknown
    }
    qa.__scanQueue = ['first', 'first', 'second']; qa.__scanCalls = 0; qa.__imageCalls = []; qa.__imports = []; qa.__scanDelay = 0
    let tokens: unknown[] = []
    const settings = { backgroundType: 'pattern', pattern: 'dots', backgroundUrl: '', backgroundName: '', opacity: .18, motion: false, theme, closeToTray: true, accentColor: '#8773b7', checkUpdatesAutomatically: false, updateAutomatically: false }
    const fixture = (key: string) => {
      if (key === 'first') return first
      if (key === 'second') return second
      if (key === 'conflict') return first.map(entry => ({ ...entry, uri: entry.uri + '-different' }))
      if (key === 'size-conflict') return second.map(entry => ({ ...entry, migration: { ...entry.migration!, size: 3 } }))
      if (key === 'too-many') return Array.from({ length: 101 }, (_, index) => ({ issuer: 'Overflow', account: `fake-${index}@example.test`, uri: `otpauth://totp/fake-${index}?secret=SYNTHETIC` }))
      if (key === 'standard') return [{ issuer: 'GitHub', account: 'regular@example.test', uri: 'otpauth://totp/GitHub:regular?secret=JBSWY3DPEHPK3PXP' }]
      throw new Error('二维码无效，请重新读取。')
    }
    qa.go = { main: { App: {
      GetState: async () => ({ tokens, settings, session: { sidebarCollapsed: false, filter: 'all', search: '' }, dataPath: '' }), GetTokens: async () => tokens,
      SaveSession: async () => {}, GetUpdateStatus: async () => ({ currentVersion: '0.3.2', latestVersion: '', phase: 'idle', progress: 0, releaseUrl: '', message: '', lastChecked: '', downloadedBytes: 0, totalBytes: 0 }),
      PreviewClipboard: async () => { qa.__scanCalls++; const key = qa.__scanQueue.shift() || 'first'; if (qa.__scanDelay) await new Promise(resolve => setTimeout(resolve, qa.__scanDelay)); return fixture(key) },
      PreviewText: async (text: string) => text.trim().split(/\s+/).flatMap(fixture),
      PreviewImage: async (data: string) => { const key = atob(data); qa.__imageCalls.push(key); return fixture(key) },
      ImportTokens: async (uris: string[]) => {
        qa.__imports.push(uris)
        const entries = uris.flatMap(uri => uri === first[0].uri ? first : uri === second[0].uri ? second : fixture('standard'))
        tokens = entries.map((entry, index) => ({ id: `import-${index}`, issuer: entry.issuer, account: entry.account, group: '', favorite: false, color: '#8581d8', algorithm: 'SHA1', digits: 6, period: 30, code: '123456', remaining: 20 }))
        return tokens
      },
    } } }
  }, { first, second, theme })
  await page.goto('/')
}

const openImport = (page: Page) => page.locator('.toolbar').getByRole('button', { name: '添加令牌', exact: true }).click()
const continuePaste = (page: Page) => page.getByRole('button', { name: '继续粘贴', exact: true }).click()

test('migration collection deduplicates pages, guards conflicts and preserves the original data on rejection', () => {
  const existing = mergeImportPreviews([], first)
  expect(mergeImportPreviews(existing, first)).toEqual(first)
  expect(migrationProgress(existing)).toEqual([{ id: 'synthetic-export', size: 2, pages: 1, complete: false }])
  expect(migrationProgress(mergeImportPreviews(second, first))).toEqual([{ id: 'synthetic-export', size: 2, pages: 2, complete: true }])
  expect(() => mergeImportPreviews(existing, second.map(entry => ({ ...entry, migration: { ...entry.migration!, size: 3 } })))).toThrow('页数不一致')
  expect(() => mergeImportPreviews(existing, first.map(entry => ({ ...entry, uri: 'different' })))).toThrow('内容冲突')
  expect(() => mergeImportPreviews(existing, Array.from({ length: 99 }, (_, index) => ({ issuer: '', account: '', uri: `fixture:${index}` })))).toThrow('100')
  expect(existing).toEqual(first)
  expect(mergeImportPreviews([], [{ issuer: 'regular', account: '', uri: 'same' }, { issuer: 'regular', account: '', uri: 'same' }])).toHaveLength(1)
})

test('multiple Google export pages collect across scans and tabs, import each source once and clear on success', async ({ page }) => {
  await installBridge(page)
  await page.locator('.toolbar').getByRole('button', { name: '粘贴二维码', exact: true }).click()
  await expect(page.locator('.migration-progress')).toHaveText('已读取 1/2 张二维码')
  await expect(page.locator('.import-preview')).toHaveCount(2)
  await expect(page.getByRole('button', { name: '确认导入', exact: true })).toBeDisabled()
  await continuePaste(page)
  await expect(page.locator('.import-preview')).toHaveCount(2)
  await page.getByRole('tab', { name: '手动输入', exact: true }).click()
  await page.getByRole('tab', { name: '二维码 / 链接', exact: true }).click()
  await expect(page.locator('.migration-progress')).toHaveText('已读取 1/2 张二维码')
  await continuePaste(page)
  await expect(page.locator('.migration-progress')).toHaveText('已读取 2/2 张二维码')
  await expect(page.locator('.import-preview')).toHaveCount(3)
  await page.getByRole('button', { name: '确认导入', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.locator('.token-card')).toHaveCount(3)
  expect(await page.evaluate(() => (window as typeof window & { __imports: string[][] }).__imports)).toEqual([[pageURI(0), pageURI(1)]])
  await openImport(page)
  await expect(page.locator('.import-preview')).toHaveCount(0)
  await expect(page.getByRole('textbox', { name: '令牌链接', exact: true })).toHaveValue('')
  expect(await page.evaluate(() => JSON.stringify(localStorage))).not.toContain('example.test')
  expect(await page.evaluate(() => JSON.stringify(localStorage))).not.toContain('otpauth')
})

test('conflicting, invalid and oversized scans preserve accepted pages; cancellation discards the batch', async ({ page }) => {
  await installBridge(page)
  await openImport(page)
  await page.getByRole('button', { name: '读取剪贴板', exact: true }).click()
  for (const key of ['conflict', 'size-conflict', 'bad', 'too-many']) {
    await page.evaluate(key => { (window as typeof window & { __scanQueue: string[] }).__scanQueue = [key] }, key)
    await continuePaste(page)
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(page.locator('.import-preview')).toHaveCount(2)
    await expect(page.locator('.migration-progress')).toHaveText('已读取 1/2 张二维码')
    await expect(page.getByRole('button', { name: '确认导入', exact: true })).toBeDisabled()
  }
  await page.getByRole('button', { name: '关闭弹窗', exact: true }).click()
  await openImport(page)
  await expect(page.locator('.import-preview')).toHaveCount(0)
  await page.getByRole('textbox', { name: '令牌链接', exact: true }).fill('second')
  await page.getByRole('button', { name: '识别链接', exact: true }).click()
  await expect(page.locator('.import-preview')).toHaveCount(1)
  await expect(page.locator('.migration-progress')).toHaveText('已读取 1/2 张二维码')
  await page.locator('.import-more-links summary').click()
  await page.getByRole('textbox', { name: '令牌链接', exact: true }).fill('first standard')
  await page.getByRole('button', { name: '识别链接', exact: true }).click()
  await expect(page.locator('.import-preview')).toHaveCount(4)
  await expect(page.getByRole('button', { name: '确认导入', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: '重新选择', exact: true }).click()
  await expect(page.getByRole('textbox', { name: '令牌链接', exact: true })).toHaveValue('')
  await expect(page.locator('.import-preview')).toHaveCount(0)
})

test('multiple image files merge atomically and dropped images can complete an existing batch', async ({ page }) => {
  await installBridge(page)
  await openImport(page)
  await page.getByRole('button', { name: '读取剪贴板', exact: true }).click()
  await page.locator('input[type=file]').setInputFiles([{ name: 'second.png', mimeType: 'image/png', buffer: Buffer.from('second') }, { name: 'bad.png', mimeType: 'image/png', buffer: Buffer.from('bad') }])
  await expect(page.getByRole('alert')).toHaveText('二维码无效，请重新读取。')
  await expect(page.locator('.import-preview')).toHaveCount(2)
  await expect(page.locator('.migration-progress')).toHaveText('已读取 1/2 张二维码')
  await page.locator('.import-flow').evaluate(element => {
    const transfer = new DataTransfer()
    transfer.items.add(new File(['second'], 'second.png', { type: 'image/png' }))
    transfer.items.add(new File(['first'], 'first.png', { type: 'image/png' }))
    element.dispatchEvent(new DragEvent('drop', { dataTransfer: transfer, bubbles: true, cancelable: true }))
  })
  await expect(page.locator('.migration-progress')).toHaveText('已读取 2/2 张二维码')
  await expect(page.locator('.import-preview')).toHaveCount(3)
  expect(await page.evaluate(() => (window as typeof window & { __imageCalls: string[] }).__imageCalls)).toEqual(['second', 'bad', 'second', 'first'])
  await page.getByRole('button', { name: '重新选择', exact: true }).click()
  await page.locator('input[type=file]').setInputFiles([{ name: 'first.png', mimeType: 'image/png', buffer: Buffer.from('first') }, { name: 'second.png', mimeType: 'image/png', buffer: Buffer.from('second') }])
  await expect(page.locator('.import-preview')).toHaveCount(3)
  await expect(page.getByRole('button', { name: '确认导入', exact: true })).toBeEnabled()
})

test('rapid repeat scans cannot overlap and close is disabled while reading', async ({ page }) => {
  await installBridge(page)
  await openImport(page)
  await page.evaluate(() => {
    (window as typeof window & { __scanDelay: number }).__scanDelay = 400
    const button = [...document.querySelectorAll('button')].find(item => item.textContent?.includes('读取剪贴板'))!
    button.click(); button.click()
  })
  await expect(page.getByRole('button', { name: '关闭弹窗', exact: true })).toBeDisabled()
  await expect(page.locator('.import-preview')).toHaveCount(2)
  expect(await page.evaluate(() => (window as typeof window & { __scanCalls: number }).__scanCalls)).toBe(1)
})

for (const theme of ['regular', 'anime'] as const) {
  test(`${theme}: migration progress and continuation controls fit desktop and narrow dialogs`, async ({ page }) => {
    await installBridge(page, theme)
    await openImport(page)
    await page.getByRole('button', { name: '读取剪贴板', exact: true }).click()
    for (const [width, height] of [[1180, 800], [360, 740]]) {
      await page.setViewportSize({ width, height })
      await expect(page.getByRole('button', { name: '继续粘贴', exact: true })).toBeVisible()
      await expect(page.getByRole('button', { name: '添加图片', exact: true })).toBeVisible()
      await expect.poll(() => page.getByRole('dialog').evaluate(element => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1)
      await page.screenshot({ path: `../output/migration-${theme}-${width}.png`, animations: 'disabled' })
    }
  })
}
