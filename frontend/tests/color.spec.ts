import { expect, test, type Page } from '@playwright/test'

const appearance = (page: Page) => page.getByRole('button', { name: '外观与背景', exact: true })
const hexInput = (page: Page) => page.getByRole('textbox', { name: '自定义颜色 HEX', exact: true })
const primary = (page: Page) => page.locator('.toolbar .primary-action')

async function setHex(page: Page, hex: string) {
  await hexInput(page).fill(hex)
  await hexInput(page).press('Tab')
}

async function colorSamples(page: Page) {
  return page.evaluate(() => {
    const style = (selector: string) => getComputedStyle(document.querySelector(selector)!)
    return {
      button: style('.toolbar .primary-action').backgroundColor,
      nav: style('.main-nav button.active').color,
      navBackground: style('.main-nav button.active').backgroundColor,
      wordmark: style('.brand strong').color,
      icon: style('.appearance-link svg').color,
      brands: [...document.querySelectorAll('.token-card')].map(card => ({
        provider: getComputedStyle(card.querySelector('.provider-icon')!).color,
        providerBackground: getComputedStyle(card.querySelector('.provider-icon')!).backgroundColor,
        track: getComputedStyle(card.querySelector('.card-time-track > span')!).backgroundColor,
      })),
      images: [...document.querySelectorAll<HTMLImageElement>('.brand img, .titlebar img')].map(image => ({ src: image.currentSrc, filter: getComputedStyle(image).filter })),
    }
  })
}

async function formSamples(page: Page) {
  await page.locator('.toolbar').getByRole('button', { name: '添加令牌', exact: true }).click()
  await page.getByRole('tab', { name: '手动输入', exact: true }).click()
  await page.getByPlaceholder('例如 Google、GitHub').focus()
  await page.locator('[role="dialog"]').evaluate(async modal => {
    await Promise.all(modal.getAnimations({ subtree: true }).filter(animation => Number.isFinite(Number(animation.effect?.getComputedTiming().endTime))).map(animation => animation.finished.catch(() => {})))
  })
  const samples = await page.evaluate(() => {
    const style = (selector: string) => getComputedStyle(document.querySelector(selector)!)
    return {
      title: style('[role="dialog"] h2').color,
      tab: style('[role="tab"][aria-selected="true"]').color,
      tabIcon: style('[role="tab"][aria-selected="true"] svg').color,
      focusBorder: style('input[placeholder="例如 Google、GitHub"]').borderColor,
      focusShadow: style('input[placeholder="例如 Google、GitHub"]').boxShadow,
    }
  })
  await page.keyboard.press('Escape')
  await expect(page.locator('[role="dialog"]')).toHaveCount(0)
  return samples
}

async function contrastOf(page: Page, selector: string) {
  const { foreground, background } = await page.locator(selector).evaluate(element => {
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = 1
    const context = canvas.getContext('2d')!
    const rgba = (color: string) => {
      context.clearRect(0, 0, 1, 1)
      context.fillStyle = color
      context.fillRect(0, 0, 1, 1)
      return [...context.getImageData(0, 0, 1, 1).data]
    }
    const ancestors: Element[] = []
    for (let current: Element | null = element; current; current = current.parentElement) ancestors.unshift(current)
    let background = [255, 255, 255]
    for (const ancestor of ancestors) {
      const next = rgba(getComputedStyle(ancestor).backgroundColor)
      background = background.map((channel, index) => next[index] * next[3] / 255 + channel * (1 - next[3] / 255))
    }
    return { foreground: rgba(getComputedStyle(element).color).slice(0, 3), background }
  })
  const luminance = (rgb: number[]) => rgb.map(channel => channel / 255).map(channel => channel <= .04045 ? channel / 12.92 : ((channel + .055) / 1.055) ** 2.4).reduce((total, channel, index) => total + channel * [.2126, .7152, .0722][index], 0)
  const a = luminance(foreground)
  const b = luminance(background)
  return (Math.max(a, b) + .05) / (Math.min(a, b) + .05)
}

test('custom accent previews globally, cancels, saves and survives switching icon/font themes', async ({ page }) => {
  await page.goto('/?demo')
  await expect(page.locator('.token-card')).toHaveCount(6)
  const before = await colorSamples(page)
  const formBefore = await formSamples(page)
  await appearance(page).click()
  await page.getByRole('button', { name: '点缀色 蓝色', exact: true }).click()
  await expect.poll(async () => (await colorSamples(page)).button).not.toBe(before.button)
  const preview = await colorSamples(page)
  for (const key of ['button', 'nav', 'navBackground', 'wordmark', 'icon'] as const) expect(preview[key], key).not.toBe(before[key])
  expect(preview.brands).toEqual(before.brands)
  expect(preview.images).toEqual(before.images)
  await page.keyboard.press('Escape')
  await expect(page.locator('[role="dialog"]')).toHaveCount(0)
  await expect.poll(() => colorSamples(page)).toEqual(before)

  await appearance(page).click()
  await setHex(page, '#167e92')
  await page.getByRole('button', { name: '保存设置', exact: true }).click()
  await expect(page.locator('[role="dialog"]')).toHaveCount(0)
  await expect(primary(page)).toHaveCSS('background-color', 'rgb(22, 126, 146)')
  const saved = await colorSamples(page)
  const formSaved = await formSamples(page)
  for (const key of ['title', 'tab', 'tabIcon', 'focusBorder', 'focusShadow'] as const) expect(formSaved[key], key).not.toBe(formBefore[key])
  await appearance(page).click()
  await page.getByRole('radio', { name: '二次元主题', exact: true }).click()
  await page.getByRole('button', { name: '保存设置', exact: true }).click()
  await expect(page.locator('[role="dialog"]')).toHaveCount(0)
  await expect(primary(page)).toHaveCSS('background-color', 'rgb(22, 126, 146)')
  expect((await colorSamples(page)).nav).toBe(saved.nav)
  await appearance(page).click()
  await expect(hexInput(page)).toHaveValue(/#167e92/i)
  await page.keyboard.press('Escape')
  await expect(page.locator('[role="dialog"]')).toHaveCount(0)
  await page.screenshot({ path: '../output/custom-accent-anime.png', fullPage: true, animations: 'disabled' })
})

test('custom color pad supports pointer and keyboard editing, valid HEX and narrow layouts', async ({ page }) => {
  await page.goto('/?demo')
  await appearance(page).click()
  for (const width of [1180, 760, 360]) {
    await page.setViewportSize({ width, height: 800 })
    await expect.poll(() => page.evaluate(() => Math.max(document.documentElement.scrollWidth - innerWidth, document.querySelector('[role="dialog"]')!.scrollWidth - document.querySelector('[role="dialog"]')!.clientWidth))).toBeLessThanOrEqual(1)
  }
  const pad = page.getByRole('slider', { name: '颜色饱和度与亮度', exact: true })
  await pad.scrollIntoViewIfNeeded()
  const before = await hexInput(page).inputValue()
  const box = (await pad.boundingBox())!
  await page.mouse.move(box.x + box.width * .6, box.y + box.height * .35)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width * .7, box.y + box.height * .25)
  await page.mouse.up()
  await expect(hexInput(page)).not.toHaveValue(before)
  const pointerColor = await hexInput(page).inputValue()
  await pad.focus()
  await pad.press('ArrowLeft')
  await pad.press('ArrowUp')
  await expect(hexInput(page)).not.toHaveValue(pointerColor)
  const keyboardColor = await hexInput(page).inputValue()
  await page.getByRole('slider', { name: '色相', exact: true }).focus()
  await page.getByRole('slider', { name: '色相', exact: true }).press('ArrowRight')
  await expect(hexInput(page)).not.toHaveValue(keyboardColor)
  const currentHex = (await hexInput(page).inputValue()).replace('#', '')
  await expect(primary(page)).toHaveCSS('background-color', `rgb(${[0, 2, 4].map(offset => parseInt(currentHex.slice(offset, offset + 2), 16)).join(', ')})`)
  const validBackground = await primary(page).evaluate(element => getComputedStyle(element).backgroundColor)
  await setHex(page, '#zzzzzz')
  await expect(primary(page)).toHaveCSS('background-color', validBackground)
  await setHex(page, '#056b39')
  await expect(primary(page)).toHaveCSS('background-color', 'rgb(5, 107, 57)')
  await pad.scrollIntoViewIfNeeded()
  await page.screenshot({ path: '../output/custom-accent-picker-mobile.png', fullPage: true, animations: 'disabled' })
  await page.getByRole('button', { name: '保存设置', exact: true }).click()
  await expect(page.locator('[role="dialog"]')).toHaveCount(0)
  await expect(primary(page)).toHaveCSS('background-color', 'rgb(5, 107, 57)')
})

test('black, white and bright yellow accents keep controls readable in both themes', async ({ page }) => {
  test.setTimeout(60000)
  await page.goto('/?demo')
  for (const theme of ['常规主题', '二次元主题']) {
    for (const color of ['#000000', '#ffffff', '#ffff00']) {
      await appearance(page).click()
      await page.getByRole('radio', { name: theme, exact: true }).click()
      await setHex(page, color)
      await page.getByRole('button', { name: '保存设置', exact: true }).click()
      await expect(page.locator('[role="dialog"]')).toHaveCount(0)
      expect(await contrastOf(page, '.toolbar .primary-action'), `${theme} ${color} primary contrast`).toBeGreaterThanOrEqual(4.5)
      expect(await contrastOf(page, '.main-nav button.active'), `${theme} ${color} active navigation contrast`).toBeGreaterThanOrEqual(4.5)
      await page.locator('.toolbar').getByRole('button', { name: '添加令牌', exact: true }).click()
      await page.getByRole('tab', { name: '手动输入', exact: true }).click()
      expect(await contrastOf(page, '[role="tab"][aria-selected="true"]'), `${theme} ${color} selected tab contrast`).toBeGreaterThanOrEqual(4.5)
      await page.keyboard.press('Escape')
      await expect(page.locator('[role="dialog"]')).toHaveCount(0)
    }
  }
})
