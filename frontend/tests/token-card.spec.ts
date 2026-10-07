import { expect, test, type Page } from '@playwright/test'

const brands = ['OpenAI', 'Google', 'Microsoft', 'GitHub', 'Apple', 'Amazon', 'Discord', 'Telegram', 'Dropbox', 'Notion', 'Figma']
const longAccount = `${'long.account.'.repeat(12)}@example.test`

async function installBridge(page: Page, theme: 'regular' | 'anime' = 'regular') {
  await page.addInitScript(({ brands, longAccount, theme }) => {
    const tokens = [...brands, 'constructor', 'OpenAI helper'].map((issuer, index) => ({
      id: `fixture-${index}`, issuer, account: index === 0 ? 'alice@example.test' : index === 1 ? longAccount : `account-${index}@example.test`,
      group: index === 0 ? 'OpenAI' : '示例', favorite: false, color: '#8581d8', algorithm: 'SHA1', digits: 6, period: 30, code: '123456', remaining: 23,
    }))
    const qa = window as typeof window & { __copies: { type: string; id: string; text: string }[]; __failAccount: boolean; go: unknown }
    qa.__copies = []
    qa.__failAccount = false
    const settings = { backgroundType: 'pattern', pattern: 'dots', backgroundUrl: '', backgroundName: '', opacity: .18, motion: false, theme, closeToTray: true, accentColor: '#8773b7', checkUpdatesAutomatically: false, updateAutomatically: false }
    qa.go = { main: { App: {
      GetState: async () => ({ tokens, settings, session: { sidebarCollapsed: false, filter: 'all', search: '' }, dataPath: '' }),
      GetTokens: async () => tokens,
      SaveSession: async () => {},
      GetUpdateStatus: async () => ({ currentVersion: '0.3.1', latestVersion: '', phase: 'idle', progress: 0, releaseUrl: '', message: '', lastChecked: '', downloadedBytes: 0, totalBytes: 0 }),
      CopyAccount: async (id: string) => {
        if (qa.__failAccount) throw new Error('复制失败，请重试。')
        qa.__copies.push({ type: 'account', id, text: tokens.find(token => token.id === id)!.account })
      },
      CopyCode: async (id: string) => { qa.__copies.push({ type: 'code', id, text: tokens.find(token => token.id === id)!.code }) },
    } } }
  }, { brands, longAccount, theme })
}

test('email copies by token ID independently of its verification code and reports failures', async ({ page }) => {
  await installBridge(page)
  await page.goto('/')
  const card = page.locator('.token-card').filter({ has: page.getByRole('heading', { name: 'OpenAI', exact: true }) })
  const email = card.getByRole('button', { name: '复制邮箱 alice@example.test', exact: true })
  await email.click()
  await expect(email).toHaveClass(/is-account-copied/)
  await expect(card).not.toHaveClass(/is-copied/)
  await expect(page.getByRole('status')).toHaveText('邮箱已复制')
  await expect.poll(() => page.evaluate(() => (window as typeof window & { __copies: unknown[] }).__copies)).toEqual([{ type: 'account', id: 'fixture-0', text: 'alice@example.test' }])
  await card.getByRole('button', { name: '复制 OpenAI 验证码', exact: true }).click()
  await expect(card).toHaveClass(/is-copied/)
  await expect(email).toHaveClass(/is-account-copied/)
  await email.focus()
  await page.keyboard.press('Enter')
  await expect.poll(() => page.evaluate(() => (window as typeof window & { __copies: unknown[] }).__copies)).toEqual([
    { type: 'account', id: 'fixture-0', text: 'alice@example.test' }, { type: 'code', id: 'fixture-0', text: '123456' }, { type: 'account', id: 'fixture-0', text: 'alice@example.test' },
  ])
  await page.evaluate(() => { (window as typeof window & { __failAccount: boolean }).__failAccount = true })
  await page.getByRole('button', { name: `复制邮箱 ${longAccount}`, exact: true }).click()
  await expect(page.getByRole('status')).toHaveText('复制失败，请重试。')
  await expect(page.locator('.account-copy.is-account-copied')).toHaveCount(1)
  expect(await page.evaluate(() => (window as typeof window & { __copies: unknown[] }).__copies.length)).toBe(3)
})

test('OpenAI manual entry and import use the default group and the same embedded logo', async ({ page }) => {
  await page.goto('/?demo')
  await expect(page.locator('.token-card')).toHaveCount(6)
  await page.locator('.toolbar').getByRole('button', { name: '添加令牌', exact: true }).click()
  await page.getByRole('tab', { name: '手动输入', exact: true }).click()
  await page.getByPlaceholder('例如 Google、GitHub').fill('open-ai')
  await page.getByPlaceholder('邮箱或用户名').fill('manual@example.test')
  await page.getByPlaceholder('粘贴服务提供的验证密钥').fill('JBSWY3DPEHPK3PXP')
  await page.getByRole('dialog').getByRole('button', { name: '添加令牌', exact: true }).click()
  const manualCard = page.locator('.token-card').filter({ has: page.getByRole('heading', { name: 'open-ai', exact: true }) })
  await expect(manualCard.locator('.group-tag')).toHaveText('OpenAI')
  await expect(manualCard.locator('.provider-glyph')).toHaveAttribute('data-brand', 'openai')
  const expectedPath = await manualCard.locator('.provider-glyph path').getAttribute('d')
  await page.locator('.toolbar').getByRole('button', { name: '添加令牌', exact: true }).click()
  await page.getByRole('textbox', { name: '令牌链接', exact: true }).fill('otpauth://totp/OpenAI%3Aimport%40example.test?secret=JBSWY3DPEHPK3PXP&issuer=OpenAI')
  await page.getByRole('button', { name: '识别链接', exact: true }).click()
  await expect(page.locator('.preview-provider .provider-glyph')).toHaveAttribute('data-brand', 'openai')
  await expect(page.locator('.preview-provider path')).toHaveAttribute('d', expectedPath!)
  await page.getByRole('button', { name: '确认导入', exact: true }).click()
  await page.getByRole('navigation', { name: '令牌分组' }).getByRole('button', { name: 'OpenAI', exact: true }).click()
  await expect(page.locator('.token-card')).toHaveCount(2)
  await expect(page.locator('.token-card .group-tag')).toHaveText(['OpenAI', 'OpenAI'])
  const custom = await page.evaluate(async moduleUrl => {
    const { api } = await import(moduleUrl)
    const token = (await api.GetTokens()).find((item: { issuer: string }) => item.issuer === 'open-ai')
    return api.UpdateToken(token.id, { ...token, group: '我的分组', secret: '' })
  }, '/src/api.ts')
  expect(custom.group).toBe('我的分组')
  await expect(page.locator('.token-card')).toHaveCount(1)
})

for (const theme of ['regular', 'anime'] as const) {
  test(`${theme}: offline brand icons and long email controls fit desktop and narrow cards`, async ({ page }) => {
    await installBridge(page, theme)
    const externalRequests: string[] = []
    page.on('request', request => { if (!request.url().startsWith('http://127.0.0.1:5177/')) externalRequests.push(request.url()) })
    await page.goto('/')
    await expect(page.locator('.token-card')).toHaveCount(13)
    await expect(page.locator('.token-card .provider-glyph')).toHaveCount(11)
    await expect(page.locator('.provider-initials')).toHaveText(['CO', 'OP'])
    for (const [width, height] of [[1180, 800], [360, 740]]) {
      await page.setViewportSize({ width, height })
      if (width === 360) await page.getByRole('button', { name: '收起侧栏', exact: true }).click()
      await expect.poll(() => page.evaluate(() => {
        const main = document.querySelector('.main-content')!
        return Math.max(document.documentElement.scrollWidth - innerWidth, main.scrollWidth - main.clientWidth)
      })).toBeLessThanOrEqual(1)
      const account = page.getByRole('button', { name: `复制邮箱 ${longAccount}`, exact: true })
      await expect(account).toBeVisible()
      const boxes = await account.evaluate(button => {
        const label = button.closest('.card-label')!.getBoundingClientRect()
        const box = button.getBoundingClientRect()
        return { right: box.right, labelRight: label.right, height: box.height }
      })
      expect(boxes.right).toBeLessThanOrEqual(boxes.labelRight + 1)
      expect(boxes.height).toBeGreaterThanOrEqual(24)
      await account.click()
      await expect(account).toHaveClass(/is-account-copied/)
      await page.locator('.main-content').evaluate(element => { element.scrollTop = 0 })
      await page.screenshot({ path: `../output/token-cards-${theme}-${width}.png`, animations: 'disabled' })
    }
    expect(externalRequests).toEqual([])
  })
}
