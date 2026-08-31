export interface CanvasArchiveEntry {
  name: string
  data: BlobPart
  type?: string
}

const textEncoder = new TextEncoder()

function crc32(bytes: Uint8Array): number {
  let crc = 0xffffffff
  for (const byte of bytes) {
    crc ^= byte
    for (let bit = 0; bit < 8; bit += 1) crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0)
  }
  return (crc ^ 0xffffffff) >>> 0
}

function write16(view: DataView, offset: number, value: number): void {
  view.setUint16(offset, value, true)
}

function write32(view: DataView, offset: number, value: number): void {
  view.setUint32(offset, value >>> 0, true)
}

function read16(view: DataView, offset: number): number {
  return view.getUint16(offset, true)
}

function read32(view: DataView, offset: number): number {
  return view.getUint32(offset, true)
}

async function bytesFor(data: BlobPart): Promise<Uint8Array> {
  if (data instanceof Blob) return new Uint8Array(await data.arrayBuffer())
  if (typeof data === 'string') return textEncoder.encode(data)
  return new Uint8Array(data as ArrayBufferLike)
}

function readBlobArrayBuffer(blob: Blob): Promise<ArrayBuffer> {
  if (typeof blob.arrayBuffer === 'function') return blob.arrayBuffer()
  if (typeof FileReader === 'undefined') return Promise.reject(new Error('Unable to read canvas archive.'))
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(reader.result as ArrayBuffer)
    reader.onerror = () => reject(reader.error ?? new Error('Unable to read canvas archive.'))
    reader.readAsArrayBuffer(blob)
  })
}

export async function createCanvasArchive(entries: CanvasArchiveEntry[]): Promise<Blob> {
  const files = await Promise.all(entries.map(async (entry) => ({ ...entry, bytes: await bytesFor(entry.data) })))
  const localParts: Uint8Array[] = []
  const centralParts: Uint8Array[] = []
  let offset = 0
  for (const file of files) {
    const name = textEncoder.encode(file.name)
    const local = new Uint8Array(30 + name.length + file.bytes.length)
    const localView = new DataView(local.buffer)
    write32(localView, 0, 0x04034b50)
    write16(localView, 4, 20)
    write16(localView, 6, 0x0800)
    write16(localView, 8, 0)
    write16(localView, 10, 0)
    write16(localView, 12, 0)
    write32(localView, 14, crc32(file.bytes))
    write32(localView, 18, file.bytes.length)
    write32(localView, 22, file.bytes.length)
    write16(localView, 26, name.length)
    write16(localView, 28, 0)
    local.set(name, 30)
    local.set(file.bytes, 30 + name.length)
    localParts.push(local)

    const central = new Uint8Array(46 + name.length)
    const centralView = new DataView(central.buffer)
    write32(centralView, 0, 0x02014b50)
    write16(centralView, 4, 20)
    write16(centralView, 6, 20)
    write16(centralView, 8, 0x0800)
    write16(centralView, 10, 0)
    write16(centralView, 12, 0)
    write16(centralView, 14, 0)
    write32(centralView, 16, crc32(file.bytes))
    write32(centralView, 20, file.bytes.length)
    write32(centralView, 24, file.bytes.length)
    write16(centralView, 28, name.length)
    write16(centralView, 30, 0)
    write16(centralView, 32, 0)
    write16(centralView, 34, 0)
    write16(centralView, 36, 0)
    write32(centralView, 38, 0)
    write32(centralView, 42, offset)
    central.set(name, 46)
    centralParts.push(central)
    offset += local.length
  }
  const centralSize = centralParts.reduce((sum, part) => sum + part.length, 0)
  const end = new Uint8Array(22)
  const endView = new DataView(end.buffer)
  write32(endView, 0, 0x06054b50)
  write16(endView, 8, files.length)
  write16(endView, 10, files.length)
  write32(endView, 12, centralSize)
  write32(endView, 16, offset)
  write16(endView, 20, 0)
  return new Blob([...localParts, ...centralParts, end], { type: 'application/zip' })
}

export async function readCanvasArchive(blob: Blob): Promise<Map<string, Blob>> {
  const bytes = new Uint8Array(await readBlobArrayBuffer(blob))
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const files = new Map<string, Blob>()
  let offset = 0
  while (offset + 4 <= bytes.length && read32(view, offset) === 0x04034b50) {
    if (offset + 30 > bytes.length) throw new Error('Invalid canvas archive.')
    const flags = read16(view, offset + 6)
    const method = read16(view, offset + 8)
    const compressedSize = read32(view, offset + 18)
    const nameLength = read16(view, offset + 26)
    const extraLength = read16(view, offset + 28)
    if (flags & 0x08 || method !== 0) throw new Error('Unsupported canvas archive compression.')
    const nameBytes = bytes.slice(offset + 30, offset + 30 + nameLength)
    const name = new TextDecoder().decode(nameBytes)
    const start = offset + 30 + nameLength + extraLength
    const end = start + compressedSize
    if (end > bytes.length) throw new Error('Invalid canvas archive entry.')
    files.set(name, new Blob([bytes.slice(start, end)]))
    offset = end
  }
  if (!files.has('projects.json')) throw new Error('Canvas archive is missing projects.json.')
  return files
}
