import { test, expect } from '@playwright/test'

test('exports all 2FA links as a copyable long string and a TXT file', async ({ page }) => {
  await page.goto('/?demo')
  await page.evaluate(async moduleUrl => {
    const { api } = await import(moduleUrl)
    api.ExportTokens = async () => 'otpauth://totp/OpenAI:alice%40example.test?secret=JBSWY3DPEHPK3PXP&issuer=OpenAI\notpauth://totp/GitHub:bob%40example.test?secret=KRSXG5A&issuer=GitHub'
  }, '/src/api.ts')
  await page.getByRole('button', { name: '导出密钥', exact: true }).click()
  await expect(page.getByRole('heading', { name: '导出 2FA 密钥', exact: true })).toBeVisible()
  await expect(page.getByText('导出内容包含真实密钥')).toBeVisible()
  await page.getByRole('button', { name: '生成字符串', exact: true }).click()
  const text = page.getByRole('textbox', { name: '2FA 密钥导出字符串' })
  await expect(text).toHaveValue(/OpenAI:alice/)
  await expect(page.getByRole('button', { name: '复制字符串', exact: true })).toBeEnabled()
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: '下载导出文件', exact: true }).click()
  await expect((await download).suggestedFilename()).toMatch(/^luma-2fa-export-\d{4}-\d{2}-\d{2}\.txt$/)
})
