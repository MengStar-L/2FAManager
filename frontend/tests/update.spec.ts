import { test, expect, type Page } from '@playwright/test'

async function openSettings(page: Page) {
  await page.getByRole('button', { name: '外观与背景', exact: true }).click()
  await expect(page.getByRole('heading', { name: '软件更新', exact: true })).toBeVisible()
}

async function savedPolicy(page: Page) {
  return page.evaluate(async moduleUrl => {
    const { api } = await import(moduleUrl)
    const { settings } = await api.GetState()
    return { check: settings.checkUpdatesAutomatically, update: settings.updateAutomatically }
  }, '/src/api.ts')
}

test('automatic update policy links switches and only takes effect after saving', async ({ page }) => {
  await page.goto('/?demo')
  await openSettings(page)
  const check = page.getByRole('switch', { name: '自动检查更新', exact: true })
  const update = page.getByRole('switch', { name: '自动更新', exact: true })
  await expect(check).toHaveAttribute('aria-checked', 'true')
  await expect(update).toHaveAttribute('aria-checked', 'false')
  await update.click()
  await expect(update).toHaveAttribute('aria-checked', 'true')
  expect(await savedPolicy(page)).toEqual({ check: true, update: false })
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await openSettings(page)
  await expect(update).toHaveAttribute('aria-checked', 'false')
  await check.click()
  await expect(check).toHaveAttribute('aria-checked', 'false')
  await expect(update).toHaveAttribute('aria-checked', 'false')
  await update.click()
  await expect(check).toHaveAttribute('aria-checked', 'true')
  await expect(update).toHaveAttribute('aria-checked', 'true')
  await page.getByRole('button', { name: '保存设置', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  expect(await savedPolicy(page)).toEqual({ check: true, update: true })
  await openSettings(page)
  await check.click()
  await expect(update).toHaveAttribute('aria-checked', 'false')
  expect(await savedPolicy(page)).toEqual({ check: true, update: true })
  await page.getByRole('button', { name: '保存设置', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  expect(await savedPolicy(page)).toEqual({ check: false, update: false })
})

test('download progress continues while editing, readiness is passive, and install respects unsaved settings', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto('/?demo')
  await openSettings(page)
  await expect(page.locator('.update-current-version')).toHaveText('当前版本 v0.3.2')
  await page.getByRole('button', { name: '检查更新', exact: true }).click()
  await expect(page.locator('.update-status')).toHaveAttribute('data-phase', 'checking')
  await expect(page.locator('.update-status')).toHaveAttribute('data-phase', 'available')
  await expect(page.locator('.update-status-label')).toContainText('v0.3.3')
  await page.getByRole('button', { name: '下载更新', exact: true }).click()
  const progress = page.getByRole('progressbar', { name: '更新下载进度' })
  await expect(progress).toBeVisible()
  await expect.poll(async () => Number(await progress.getAttribute('aria-valuenow'))).toBeGreaterThan(0)
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('button', { name: '添加令牌', exact: true }).click()
  await page.getByRole('tab', { name: '手动输入', exact: true }).click()
  const issuer = page.getByPlaceholder('例如 Google、GitHub')
  await issuer.fill('正在编辑的服务')
  await expect(page.locator('.toast')).toContainText('更新已下载')
  await expect(page.getByRole('dialog')).toContainText('添加令牌')
  await expect(issuer).toHaveValue('正在编辑的服务')
  await expect(issuer).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await openSettings(page)
  const install = page.getByRole('button', { name: '安装并重启', exact: true })
  await expect(install).toBeEnabled()
  await page.getByRole('switch', { name: '自动更新', exact: true }).click()
  await expect(install).toBeDisabled()
  await expect(page.getByText('请先保存当前设置，再安装更新。')).toBeVisible()
  await page.getByRole('switch', { name: '自动更新', exact: true }).click()
  await expect(install).toBeEnabled()
  await install.click()
  await expect(page.locator('.update-status')).toHaveAttribute('data-phase', 'upToDate')
  await expect(page.locator('.update-current-version')).toHaveText('当前版本 v0.3.3')
  await expect(page.locator('.update-message')).toContainText('浏览器不会重启')
  expect(errors).toEqual([])
})

test('manual update failures recover and update controls fit a narrow anime layout', async ({ page }) => {
  await page.goto('/?demo')
  await page.evaluate(async moduleUrl => {
    const { api } = await import(moduleUrl)
    const original = api.CheckForUpdates
    api.CheckForUpdates = async () => { api.CheckForUpdates = original; throw new Error('离线测试：暂时无法连接') }
  }, '/src/api.ts')
  await openSettings(page)
  await page.getByRole('button', { name: '检查更新', exact: true }).click()
  await expect(page.locator('.update-message')).toContainText('暂时无法连接')
  await expect(page.getByRole('button', { name: '检查更新', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: '检查更新', exact: true }).click()
  await expect(page.locator('.update-status')).toHaveAttribute('data-phase', 'available')
  await page.evaluate(async moduleUrl => {
    const { api } = await import(moduleUrl)
    const original = api.DownloadUpdate
    api.DownloadUpdate = async () => { api.DownloadUpdate = original; throw new Error('下载测试：连接中断') }
  }, '/src/api.ts')
  await page.getByRole('button', { name: '下载更新', exact: true }).click()
  await expect(page.locator('.update-message')).toContainText('连接中断')
  await page.getByRole('button', { name: '重试下载', exact: true }).click()
  await expect(page.locator('.update-status')).toHaveAttribute('data-phase', 'ready')
  await page.getByRole('radio', { name: '二次元主题', exact: true }).click()
  for (const width of [760, 360]) {
    await page.setViewportSize({ width, height: 740 })
    await page.locator('.update-settings').scrollIntoViewIfNeeded()
    await expect.poll(() => page.getByRole('dialog').evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1)
    const box = await page.locator('.update-settings').boundingBox()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(width + 1)
    await expect(page.getByRole('switch', { name: '自动更新', exact: true })).toBeVisible()
  }
  await page.screenshot({ path: '../output/update-mobile.png', animations: 'disabled', fullPage: true })
})

test('an older native bridge missing update methods does not break token UI', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.addInitScript(() => {
    const bridge = {
      async GetState() { return { tokens: [], settings: {}, session: { sidebarCollapsed: false, filter: 'all', search: '' }, dataPath: '' } },
      async GetTokens() { return [] },
      async SaveSession() {},
    }
    Object.assign(window, { go: { main: { App: bridge } } })
  })
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '暂无令牌', exact: true })).toBeVisible()
  await openSettings(page)
  await expect(page.locator('.update-status')).toHaveAttribute('data-phase', 'error')
  await expect(page.locator('.update-message')).toHaveText('更新服务暂不可用，请重新启动应用。')
  await page.getByRole('button', { name: '检查更新', exact: true }).click()
  await expect(page.locator('.update-message')).not.toContainText('is not a function')
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('button', { name: '添加令牌', exact: true }).click()
  await expect(page.getByRole('tab', { name: '手动输入', exact: true })).toBeVisible()
  expect(errors).toEqual([])
})
