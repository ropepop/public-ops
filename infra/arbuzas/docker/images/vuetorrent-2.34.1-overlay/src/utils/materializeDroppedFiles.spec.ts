import { materializeDroppedFiles } from './materializeDroppedFiles'

describe('materializeDroppedFiles', () => {
  it('copies file contents and metadata into new memory-backed files', async () => {
    const first = new File([new Uint8Array([1, 2, 3])], 'first.torrent', {
      type: 'application/x-bittorrent',
      lastModified: 1_234,
    })
    const second = new File([new Uint8Array([4, 5])], 'second.torrent', {
      type: 'application/octet-stream',
      lastModified: 5_678,
    })

    const materialized = await materializeDroppedFiles([first, second])

    expect(materialized).toHaveLength(2)
    expect(materialized[0]).not.toBe(first)
    expect(materialized[0].name).toBe(first.name)
    expect(materialized[0].type).toBe(first.type)
    expect(materialized[0].lastModified).toBe(first.lastModified)
    expect(materialized[0].size).toBe(first.size)
    expect(new Uint8Array(await materialized[0].arrayBuffer())).toEqual(new Uint8Array([1, 2, 3]))
    expect(materialized[1]).not.toBe(second)
    expect(materialized[1].name).toBe(second.name)
    expect(materialized[1].type).toBe(second.type)
    expect(materialized[1].lastModified).toBe(second.lastModified)
    expect(materialized[1].size).toBe(second.size)
    expect(new Uint8Array(await materialized[1].arrayBuffer())).toEqual(new Uint8Array([4, 5]))
  })

  it('starts reading every dropped file immediately', async () => {
    const first = new File([new Uint8Array([1])], 'first.torrent')
    const second = new File([new Uint8Array([2])], 'second.torrent')
    const firstRead = vi.spyOn(first, 'arrayBuffer')
    const secondRead = vi.spyOn(second, 'arrayBuffer')

    const materializing = materializeDroppedFiles([first, second])

    expect(firstRead).toHaveBeenCalledOnce()
    expect(secondRead).toHaveBeenCalledOnce()
    await materializing
  })

  it('rejects the whole batch when a dropped file cannot be read', async () => {
    const readable = new File([new Uint8Array([1])], 'readable.torrent')
    const unreadable = new File([new Uint8Array([2])], 'unreadable.torrent')
    vi.spyOn(unreadable, 'arrayBuffer').mockRejectedValue(new Error('File access expired'))

    await expect(materializeDroppedFiles([readable, unreadable])).rejects.toThrow('File access expired')
  })

  it('rejects the whole batch when copied bytes do not match the advertised size', async () => {
    const truncated = new File([new Uint8Array([1, 2, 3])], 'truncated.torrent')
    vi.spyOn(truncated, 'arrayBuffer').mockResolvedValue(new Uint8Array([1, 2]).buffer)

    await expect(materializeDroppedFiles([truncated])).rejects.toThrow('Dropped file contents do not match the advertised size')
  })

  it('accepts an empty batch without creating files', async () => {
    await expect(materializeDroppedFiles([])).resolves.toEqual([])
  })
})
