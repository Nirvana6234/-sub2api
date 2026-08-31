import { describe, expect, it } from 'vitest'
import { createCanvasArchive, readCanvasArchive } from '../canvas/canvasArchive'

function readText(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error)
    reader.readAsText(blob)
  })
}

function readBytes(blob: Blob): Promise<number[]> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(Array.from(new Uint8Array(reader.result as ArrayBuffer)))
    reader.onerror = () => reject(reader.error)
    reader.readAsArrayBuffer(blob)
  })
}

describe('canvas archive', () => {
  it('round-trips project metadata and binary media', async () => {
    const archive = await createCanvasArchive([
      { name: 'projects.json', data: '{"version":1}' },
      { name: 'assets/image.png', data: new Uint8Array([0, 1, 2, 255]) },
    ])
    expect(archive.type).toBe('application/zip')
    const files = await readCanvasArchive(archive)
    expect(await readText(files.get('projects.json')!)).toBe('{"version":1}')
    expect(await readBytes(files.get('assets/image.png')!)).toEqual([0, 1, 2, 255])
  })

  it('rejects archives that do not contain projects.json', async () => {
    const archive = await createCanvasArchive([{ name: 'readme.txt', data: 'invalid' }])
    await expect(readCanvasArchive(archive)).rejects.toThrow('projects.json')
  })
})
