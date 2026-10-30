import { test, expect, type Page } from '@playwright/test'

type Options = { copyFails?: boolean; save?: 'ok' | 'cancel' | 'fail'; deleteFails?: boolean; pendingCopy?: boolean }
async function setup(page: Page, options: Options = {}) {
  await page.goto('/?demo')
  await page.evaluate(async ({ moduleUrl, options }) => {
    const { api } = await import(moduleUrl)
    const calls = { previews: [] as string[][], copies: [] as string[], saves: [] as string[], deleted: [] as string[][], discarded: [] as string[] }
    ;(window as any).exportCalls = calls
    let snapshot: any
    const originalDelete = api.DeleteToken.bind(api)
    api.PreviewTokenExport = async (ids: string[]) => {
      calls.previews.push(ids)
      const tokens = await api.GetTokens()
      const credentials = ids.map(id => tokens.find((token: any) => token.id === id)).map((token: any) => ({ id: token.id, issuer: token.issuer, account: token.account, secret: `SYNTHETICKEY${token.id}`, uri: `otpauth://totp/${token.issuer}:${token.account}?secret=SYNTHETICKEY${token.id}` }))
      snapshot = { exportId: `export-${calls.previews.length}`, credentials, text: credentials.map((credential: any) => credential.uri).join('\n') }
      return snapshot
    }
    api.CopyTokenExport = async (id: string) => { calls.copies.push(id); if (options.pendingCopy) await new Promise(resolve => setTimeout(resolve, 600)); if (options.copyFails) throw new Error('复制失败，请重试') }
    api.SaveTokenExport = async (id: string) => { calls.saves.push(id); if (options.save === 'fail') throw new Error('文件无法写入'); return options.save !== 'cancel' }
    api.DeleteExportedTokens = async () => { if (options.deleteFails) throw new Error('账号已变更，请重新导出'); const ids = snapshot.credentials.map((credential: any) => credential.id); calls.deleted.push(ids); for (const id of ids) await originalDelete(id) }
    api.DiscardTokenExport = async (id: string) => { calls.discarded.push(id) }
  }, { moduleUrl: '/src/api.ts', options })
}
async function open(page: Page) { await page.getByRole('button', { name: '导出密钥', exact: true }).click(); await expect(page.getByRole('heading', { name: '查看与导出凭证', exact: true })).toBeVisible() }
async function select(page: Page, issuer: string, account = 'hello@luma.design') { await page.getByRole('checkbox', { name: `选择 ${issuer} ${account}`, exact: true }).check() }
async function view(page: Page) { await page.getByRole('button', { name: '查看凭证', exact: true }).click(); await expect(page.getByRole('textbox', { name: '2FA 密钥导出字符串' })).toBeVisible() }
async function calls(page: Page) { return page.evaluate(() => (window as any).exportCalls) }

test('single and multiple selection preview only matching keys, survive search, and clear on selection or dismissal', async ({ page }) => {
  await setup(page); await open(page)
  await expect(page.getByRole('button', { name: '查看凭证', exact: true })).toBeDisabled()
  await select(page, 'GitHub')
  const search = page.getByRole('textbox', { name: '搜索待导出账号' })
  await search.fill('Google'); await select(page, 'Google'); await search.clear()
  await expect(page.getByText('已选 2 个账号', { exact: true })).toBeVisible()
  await view(page)
  expect((await calls(page)).previews).toEqual([['demo-0', 'demo-1']])
  await expect(page.getByRole('textbox', { name: 'GitHub hello@luma.design 的密钥', exact: true })).toHaveValue('SYNTHETICKEYdemo-0')
  await expect(page.getByRole('textbox', { name: '2FA 密钥导出字符串' })).not.toHaveValue(/Figma/)
  await page.getByRole('button', { name: '更改选择', exact: true }).click()
  await page.getByRole('checkbox', { name: '选择 Google hello@luma.design', exact: true }).uncheck()
  await expect(page.getByRole('textbox', { name: '2FA 密钥导出字符串' })).toHaveCount(0)
  await view(page)
  expect((await calls(page)).previews[1]).toEqual(['demo-0'])
  await page.keyboard.press('Escape'); await expect(page.getByRole('dialog')).toHaveCount(0)
  expect((await calls(page)).deleted).toEqual([])
  expect((await calls(page)).discarded).toContain('export-2')
  await open(page); await expect(page.getByText('已选 0 个账号', { exact: true })).toBeVisible()
})

test('card menu can preselect a single account for credential viewing', async ({ page }) => {
  await setup(page)
  await page.getByRole('button', { name: 'Google 更多操作', exact: true }).click()
  await page.getByRole('button', { name: '查看凭证', exact: true }).click()
  await expect(page.getByRole('checkbox', { name: '选择 Google hello@luma.design', exact: true })).toBeChecked()
  await expect(page.getByRole('checkbox', { name: '选择 GitHub hello@luma.design', exact: true })).not.toBeChecked()
  await view(page); expect((await calls(page)).previews).toEqual([['demo-1']])
})

test('copy success prompts to keep accounts and clearing the dialog preserves every token', async ({ page }) => {
  await setup(page); await open(page); await select(page, 'GitHub'); await view(page)
  await page.getByRole('button', { name: '复制字符串', exact: true }).click()
  await expect(page.getByRole('heading', { name: '导出完成', exact: true })).toBeVisible()
  await expect(page.getByLabel('刚才导出的账号')).toContainText('GitHub')
  await expect(page.getByLabel('刚才导出的账号')).not.toContainText('Google')
  await expect(page.getByRole('textbox', { name: '2FA 密钥导出字符串' })).toHaveCount(0)
  await page.getByRole('button', { name: '保留账号', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  expect((await calls(page)).copies).toEqual(['export-1']); expect((await calls(page)).deleted).toEqual([])
  await expect(page.locator('.token-card')).toHaveCount(6)
})

for (const save of ['cancel', 'fail'] as const) test(`save ${save} never enables deletion`, async ({ page }) => {
  await setup(page, { save }); await open(page); await select(page, 'GitHub'); await view(page)
  await page.getByRole('button', { name: '保存 TXT', exact: true }).click()
  await expect(page.getByRole('textbox', { name: '2FA 密钥导出字符串' })).toBeVisible()
  await expect(page.getByRole('heading', { name: '导出完成', exact: true })).toHaveCount(0)
  if (save === 'fail') await expect(page.getByRole('alert')).toContainText('文件无法写入')
  expect((await calls(page)).deleted).toEqual([])
})

test('failed copy stays in preview and exposes no delete action', async ({ page }) => {
  await setup(page, { copyFails: true }); await open(page); await select(page, 'GitHub'); await view(page)
  await page.getByRole('button', { name: '复制字符串', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('复制失败')
  await expect(page.getByRole('button', { name: '删除已导出账号', exact: true })).toHaveCount(0)
  expect((await calls(page)).deleted).toEqual([])
})

test('saved export deletes only its exact accounts after explicit confirmation', async ({ page }) => {
  await setup(page, { save: 'ok' }); await open(page); await select(page, 'GitHub'); await select(page, 'Google'); await view(page)
  await page.getByRole('button', { name: '保存 TXT', exact: true }).click()
  await expect(page.getByRole('heading', { name: '导出完成', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '删除已导出账号', exact: true }).click()
  expect((await calls(page)).deleted).toEqual([])
  await expect(page.getByRole('heading', { name: '删除已导出的账号？', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '确认删除 2 个账号', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  expect((await calls(page)).deleted).toEqual([['demo-0', 'demo-1']])
  await expect(page.locator('.token-card')).toHaveCount(4)
})

test('delete failure retains the exported account prompt and dismissing keeps accounts', async ({ page }) => {
  await setup(page, { deleteFails: true }); await open(page); await select(page, 'GitHub'); await view(page)
  await page.getByRole('button', { name: '复制字符串', exact: true }).click()
  await page.getByRole('button', { name: '删除已导出账号', exact: true }).click()
  await page.getByRole('button', { name: '确认删除 1 个账号', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('账号已变更')
  await page.keyboard.press('Escape'); await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.locator('.token-card')).toHaveCount(6)
})

test('pending export blocks duplicate actions, selection and modal dismissal', async ({ page }) => {
  await setup(page, { pendingCopy: true }); await open(page); await select(page, 'GitHub'); await view(page)
  await page.getByRole('button', { name: '复制字符串', exact: true }).click()
  await expect(page.getByRole('button', { name: '关闭弹窗', exact: true })).toBeDisabled()
  await expect(page.getByRole('checkbox')).toHaveCount(0)
  await expect(page.getByRole('button', { name: '更改选择', exact: true })).toBeDisabled()
  await page.keyboard.press('Escape'); await expect(page.getByRole('dialog')).toBeVisible()
  await expect(page.getByRole('heading', { name: '导出完成', exact: true })).toBeVisible()
  expect((await calls(page)).copies).toEqual(['export-1'])
})

for (const theme of ['regular', 'anime'] as const) for (const width of [1180, 360]) test(`${theme} export selection, preview and completion fit width ${width}`, async ({ page }) => {
  await page.setViewportSize({ width, height: width === 1180 ? 800 : 900 })
  await setup(page)
  if (theme === 'anime') {
    await page.getByRole('button', { name: '外观与背景', exact: true }).click()
    await page.getByRole('radio', { name: '二次元主题', exact: true }).click()
    await page.getByRole('button', { name: '保存设置', exact: true }).click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
  }
  await open(page); await select(page, 'GitHub'); await select(page, 'Google'); await view(page)
  expect(await page.getByRole('dialog').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBeTruthy()
  for (const name of ['复制字符串', '保存 TXT']) {
    const button = page.getByRole('button', { name, exact: true })
    await button.scrollIntoViewIfNeeded()
    const bounds = await button.boundingBox()
    expect(bounds?.x).toBeGreaterThanOrEqual(0); expect((bounds?.x || 0) + (bounds?.width || 0)).toBeLessThanOrEqual(width)
  }
  const close = await page.getByRole('button', { name: '关闭弹窗', exact: true }).boundingBox()
  expect(close?.y).toBeGreaterThanOrEqual(0)
  const heading = await page.getByRole('heading', { name: '查看与导出凭证', exact: true }).boundingBox()
  expect(heading?.y).toBeGreaterThanOrEqual(0)
  if ((theme === 'regular' && width === 1180) || (theme === 'anime' && width === 360)) await page.screenshot({ path: `../output/selected-export-${theme}-${width}.png` })
  await page.getByRole('button', { name: '复制字符串', exact: true }).click()
  await expect(page.getByRole('heading', { name: '导出完成', exact: true })).toBeVisible()
  expect(await page.getByRole('dialog').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBeTruthy()
  await expect(page.getByRole('button', { name: '保留账号', exact: true })).toBeVisible()
})

test('selecting more than one import batch disables preview until selection fits the limit', async ({ page }) => {
  await setup(page)
  await page.evaluate(async moduleUrl => {
    const { api } = await import(moduleUrl)
    const template = (await api.GetTokens())[0]
    api.GetTokens = async () => Array.from({ length: 101 }, (_, index) => ({ ...template, id: `many-${index}`, issuer: `Synthetic ${index}`, account: `account-${index}@example.test` }))
  }, '/src/api.ts')
  await open(page)
  await expect(page.getByRole('checkbox')).toHaveCount(101)
  await page.getByRole('button', { name: '全选', exact: true }).click()
  await expect(page.getByText('已选 101 个账号', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '查看凭证', exact: true })).toBeDisabled()
  await page.getByRole('checkbox').first().uncheck()
  await expect(page.getByRole('button', { name: '查看凭证', exact: true })).toBeEnabled()
})
