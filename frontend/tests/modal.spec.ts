import { expect, test, type Page } from '@playwright/test'

const dialog = (page: Page) => page.locator('[role="dialog"]')
const addButton = (page: Page) => page.locator('.toolbar').getByRole('button', { name: '添加令牌', exact: true })

async function settleDialog(page: Page) {
  await dialog(page).waitFor()
  await dialog(page).evaluate(async element => {
    const animations = element.parentElement!.getAnimations({ subtree: true }).filter(animation =>
      Number.isFinite(Number(animation.effect?.getComputedTiming().endTime)))
    await Promise.all(animations.map(animation => animation.finished.catch(() => {})))
  })
}

async function expectCenteredClose(page: Page) {
  const offset = await page.getByRole('button', { name: '关闭弹窗' }).evaluate(button => {
    const outer = button.getBoundingClientRect()
    const icon = button.querySelector('svg')!.getBoundingClientRect()
    return { x: Math.abs(outer.x + outer.width / 2 - icon.x - icon.width / 2), y: Math.abs(outer.y + outer.height / 2 - icon.y - icon.height / 2) }
  })
  expect(offset.x).toBeLessThan(1)
  expect(offset.y).toBeLessThan(1)
}

async function sampleClose(page: Page, route: 'x' | 'escape' | 'backdrop' | 'submit' | 'repeat') {
  return page.evaluate(async route => {
    const modal = document.querySelector<HTMLElement>('[role="dialog"]')!
    const overlay = modal.parentElement!
    const close = () => modal.querySelector<HTMLButtonElement>('[aria-label="关闭弹窗"]')!.click()
    const escape = () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    const backdrop = () => overlay.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    if (route === 'x') close()
    if (route === 'escape') escape()
    if (route === 'backdrop') backdrop()
    if (route === 'submit') modal.querySelector('form')!.requestSubmit()
    if (route === 'repeat') { close(); escape(); backdrop(); close() }
    await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())))
    const moving = overlay.getAnimations({ subtree: true }).filter(animation => {
      const timing = animation.effect?.getComputedTiming()
      return animation.playState === 'running' && Number(timing?.duration) > 10 && Number.isFinite(Number(timing?.endTime))
    })
    return {
      mounted: modal.isConnected,
      visibleTransition: moving.some(animation => {
        const frames = (animation.effect as KeyframeEffect).getKeyframes()
        return frames.length >= 2 && ['opacity', 'transform', 'height'].some(property => frames[0][property] !== frames.at(-1)![property])
      }),
    }
  }, route)
}

async function expectClosedAndFocused(page: Page) {
  await expect(dialog(page)).toHaveCount(0)
  await expect(page.locator('.modal-overlay')).toHaveCount(0)
  await expect(addButton(page)).toBeFocused()
}

test('close icons stay centered in both themes and desktop/narrow dialogs', async ({ page }) => {
  await page.goto('/?demo')
  for (const theme of ['常规主题', '二次元主题']) {
    await page.getByRole('button', { name: '外观与背景', exact: true }).click()
    await page.getByRole('radio', { name: theme, exact: true }).click()
    await page.getByRole('button', { name: '保存设置', exact: true }).click()
    await expect(dialog(page)).toHaveCount(0)
    for (const width of [1180, 360]) {
      await page.setViewportSize({ width, height: 800 })
      await addButton(page).click()
      await settleDialog(page)
      await expectCenteredClose(page)
      await page.getByRole('tab', { name: '手动输入', exact: true }).click()
      await settleDialog(page)
      await expectCenteredClose(page)
      await page.getByRole('button', { name: '关闭弹窗' }).click()
      await expectClosedAndFocused(page)
      await page.getByRole('button', { name: '外观与背景', exact: true }).click()
      await settleDialog(page)
      await expectCenteredClose(page)
      await page.keyboard.press('Escape')
      await expect(dialog(page)).toHaveCount(0)
    }
  }
})

test('tabs animate real content, retain inputs and tolerate rapid reversals', async ({ page }) => {
  await page.goto('/?demo')
  await addButton(page).click()
  await settleDialog(page)
  const uri = 'otpauth://totp/Animation:preview?secret=JBSWY3DPEHPK3PXP&issuer=Animation'
  await page.getByRole('textbox', { name: '令牌链接' }).fill(uri)
  const transition = await page.evaluate(async () => {
    document.querySelector<HTMLButtonElement>('#manual-tab')!.click()
    await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())))
    const panel = document.querySelector<HTMLElement>('[role="tabpanel"]')!
    const animations = panel.getAnimations({ subtree: true }).filter(animation =>
      animation.playState === 'running' && Number(animation.effect?.getComputedTiming().duration) > 10)
    const frames = animations.flatMap(animation => (animation.effect as KeyframeEffect).getKeyframes())
    for (const animation of animations) {
      animation.pause()
      animation.currentTime = Number(animation.effect!.getComputedTiming().duration) / 2
    }
    return { count: animations.length, visual: frames.some(frame => 'transform' in frame || 'opacity' in frame), opacity: getComputedStyle(panel).opacity }
  })
  expect(transition.count).toBeGreaterThan(0)
  expect(transition.visual).toBeTruthy()
  await page.getByPlaceholder('例如 Google、GitHub').fill('切换后保留的服务')
  await page.getByPlaceholder('邮箱或用户名').fill('animation@example.test')
  await page.getByPlaceholder('粘贴服务提供的验证密钥').fill('JBSWY3DPEHPK3PXP')
  await page.screenshot({ path: '../output/modal-tab-mid-transition.png', fullPage: true, animations: 'allow' })
  await dialog(page).evaluate(element => element.getAnimations({ subtree: true }).forEach(animation => { if (animation.playState === 'paused') animation.play() }))
  await settleDialog(page)
  await expect(page.getByRole('tab', { name: '手动输入', exact: true })).toHaveAttribute('aria-selected', 'true')
  await page.getByRole('tab', { name: '二维码 / 链接', exact: true }).click()
  await settleDialog(page)
  await expect(page.getByRole('textbox', { name: '令牌链接' })).toHaveValue(uri)
  await page.evaluate(async () => {
    for (const id of ['manual-tab', 'import-tab', 'manual-tab', 'import-tab', 'manual-tab']) {
      document.getElementById(id)!.click()
      await new Promise<void>(resolve => requestAnimationFrame(() => resolve()))
    }
  })
  await settleDialog(page)
  await expect(page.getByPlaceholder('例如 Google、GitHub')).toHaveValue('切换后保留的服务')
  await expect(page.getByPlaceholder('邮箱或用户名')).toHaveValue('animation@example.test')
  await expect(page.getByPlaceholder('粘贴服务提供的验证密钥')).toHaveValue('JBSWY3DPEHPK3PXP')
  const exit = await sampleClose(page, 'repeat')
  expect(exit.mounted).toBeTruthy()
  expect(exit.visibleTransition).toBeTruthy()
  await expectClosedAndFocused(page)
  await addButton(page).click()
  await settleDialog(page)
  await expect(page.getByRole('textbox', { name: '令牌链接' })).toHaveValue('')
  await page.keyboard.press('Escape')
  await expectClosedAndFocused(page)
})

test('X, Escape, backdrop and successful submit animate out before detaching', async ({ page }) => {
  await page.goto('/?demo')
  for (const route of ['x', 'escape', 'backdrop', 'submit'] as const) {
    await addButton(page).click()
    await settleDialog(page)
    if (route === 'submit') {
      await page.getByRole('tab', { name: '手动输入', exact: true }).click()
      await settleDialog(page)
      await page.getByPlaceholder('例如 Google、GitHub').fill('动画提交验收')
      await page.getByPlaceholder('邮箱或用户名').fill('submit@example.test')
      await page.getByPlaceholder('粘贴服务提供的验证密钥').fill('JBSWY3DPEHPK3PXP')
    }
    const exit = await sampleClose(page, route)
    expect(exit.mounted, `${route} should retain the dialog while exiting`).toBeTruthy()
    expect(exit.visibleTransition, `${route} should have a running visual exit animation`).toBeTruthy()
    await expectClosedAndFocused(page)
  }
  await expect(page.locator('.token-card')).toHaveCount(7)
})

test('disabled motion and reduced-motion preference close without a lingering overlay', async ({ page }) => {
  for (const mode of ['setting', 'system'] as const) {
    await page.emulateMedia({ reducedMotion: mode === 'system' ? 'reduce' : 'no-preference' })
    await page.goto('/?demo')
    if (mode === 'setting') {
      await page.getByRole('button', { name: '外观与背景', exact: true }).click()
      await page.getByRole('switch', { name: '界面动效' }).click()
      await page.getByRole('button', { name: '保存设置', exact: true }).click()
      await expect(dialog(page)).toHaveCount(0)
    }
    await addButton(page).click()
    await page.getByRole('tab', { name: '手动输入', exact: true }).click()
    const running = await dialog(page).evaluate(element => element.getAnimations({ subtree: true }).filter(animation =>
      animation.playState === 'running' && Number(animation.effect?.getComputedTiming().duration) > 10).length)
    expect(running).toBe(0)
    await page.getByPlaceholder('例如 Google、GitHub').fill('无动效仍可编辑')
    const exit = await sampleClose(page, 'repeat')
    expect(exit.mounted).toBeFalsy()
    await expectClosedAndFocused(page)
  }
})

test('card dialogs return focus to the menu trigger or toolbar after deleting that card', async ({ page }) => {
  await page.goto('/?demo')
  const more = page.getByRole('button', { name: 'GitHub 更多操作', exact: true })
  await more.click()
  await page.getByRole('button', { name: '编辑令牌', exact: true }).click()
  await settleDialog(page)
  const exit = await sampleClose(page, 'escape')
  expect(exit.mounted && exit.visibleTransition).toBeTruthy()
  await expect(dialog(page)).toHaveCount(0)
  await expect(more).toBeFocused()
  await more.click()
  await page.getByRole('button', { name: '删除令牌', exact: true }).click()
  await settleDialog(page)
  await page.getByRole('button', { name: '取消', exact: true }).click()
  await expect(dialog(page)).toHaveCount(0)
  await expect(more).toBeFocused()
  await more.click()
  await page.getByRole('button', { name: '删除令牌', exact: true }).click()
  await settleDialog(page)
  await page.getByRole('button', { name: '确认删除', exact: true }).click()
  await expect(page.locator('.modal-overlay')).toHaveCount(0)
  await expect(page.locator('.token-card')).toHaveCount(5)
  await expect(addButton(page)).toBeFocused()
})
