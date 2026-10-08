import type { ImportPreview } from './api'

export type MigrationProgress = { id: string; size: number; pages: number; complete: boolean }

export function migrationProgress(previews: ImportPreview[]): MigrationProgress[] {
  const batches = new Map<string, { size: number; pages: Set<number> }>()
  for (const preview of previews) {
    if (!preview.migration) continue
    const { id, size, index } = preview.migration
    const batch = batches.get(id) || { size, pages: new Set<number>() }
    batch.pages.add(index)
    batches.set(id, batch)
  }
  return [...batches].map(([id, batch]) => ({ id, size: batch.size, pages: batch.pages.size, complete: batch.pages.size === batch.size }))
}

// Merge whole decoded sources together. A rejected scan never changes the accepted pages.
export function mergeImportPreviews(current: ImportPreview[], incoming: ImportPreview[]): ImportPreview[] {
  if (!incoming.length) throw new Error('没有找到可导入的令牌。')
  const sources = new Map<string, { uri: string; entries: ImportPreview[] }>()
  const batchSizes = new Map<string, number>()
  const add = (previews: ImportPreview[]) => {
    const scan = new Map<string, { uri: string; entries: ImportPreview[] }>()
    for (const preview of previews) {
      const migration = preview.migration
      if (!preview.uri) throw new Error('二维码数据不完整，请重新读取。')
      let key = `uri:${preview.uri}`
      if (migration) {
        const { id, size, index } = migration
        if (!id || !Number.isInteger(size) || size < 1 || size > 100 || !Number.isInteger(index) || index < 0 || index >= size) throw new Error('迁移二维码的分页信息无效。')
        if (batchSizes.has(id) && batchSizes.get(id) !== size) throw new Error('这组迁移二维码的页数不一致，请重新导出。')
        batchSizes.set(id, size)
        key = `migration:${id}:${index}`
      }
      const page = scan.get(key)
      if (page && page.uri !== preview.uri) throw new Error('同一页迁移二维码内容冲突，请使用同一次导出的二维码。')
      if (page) {
        if (migration) page.entries.push(preview)
      } else scan.set(key, { uri: preview.uri, entries: [preview] })
    }
    for (const [key, page] of scan) {
      const previous = sources.get(key)
      if (previous && previous.uri !== page.uri) throw new Error('同一页迁移二维码内容冲突，请使用同一次导出的二维码。')
      if (!previous) sources.set(key, page)
    }
  }
  add(current)
  add(incoming)
  const result = [...sources.values()].flatMap(source => source.entries)
  if (result.length > 100) throw new Error('一次最多导入 100 枚令牌，请分批导出。')
  return result
}
