import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import DnDZone from './DnDZone.vue'

type DropCallback = (files: File[] | null, event: DragEvent) => void

const mocks = vi.hoisted(() => ({
  addTorrents: vi.fn(),
  createDialog: vi.fn(),
  dropCallbacks: [] as DropCallback[],
  pushTorrentToQueue: vi.fn(),
  toastError: vi.fn(),
  toastPromise: vi.fn(),
}))

vi.mock('@vueuse/core', async () => {
  const { ref } = await import('vue')

  return {
    useDropZone: vi.fn((_target, options?: { onDrop?: DropCallback }) => {
      if (options?.onDrop) mocks.dropCallbacks.push(options.onDrop)
      return { isOverDropZone: ref(false) }
    }),
  }
})

vi.mock('vue-router', () => ({
  useRoute: () => ({ name: 'dashboard', params: { tab: '', subtab: '' } }),
}))

vi.mock('vue3-toastify', () => ({
  toast: {
    error: mocks.toastError,
    promise: mocks.toastPromise,
  },
}))

vi.mock('@/composables', () => ({
  useI18nUtils: () => ({ t: (key: string) => key }),
}))

vi.mock('@/stores', () => ({
  useAddTorrentStore: () => ({ pushTorrentToQueue: mocks.pushTorrentToQueue }),
  useAppStore: () => ({ isAuthenticated: true }),
  useDialogStore: () => ({ hasActiveDialog: false, createDialog: mocks.createDialog }),
  useTorrentStore: () => ({ addTorrents: mocks.addTorrents }),
}))

vi.mock('./Dialogs/AddTorrentDialog.vue', () => ({
  default: { template: '<div />' },
}))

function createDropEvent(text = ''): DragEvent {
  return {
    dataTransfer: {
      getData: vi.fn(() => text),
    },
    preventDefault: vi.fn(),
  } as unknown as DragEvent
}

describe('DnDZone dropped file materialization', () => {
  let wrapper: VueWrapper

  beforeEach(() => {
    mocks.addTorrents.mockReset().mockResolvedValue(undefined)
    mocks.createDialog.mockReset()
    mocks.dropCallbacks.length = 0
    mocks.pushTorrentToQueue.mockReset()
    mocks.toastError.mockReset()
    mocks.toastPromise.mockReset().mockImplementation((promise: Promise<unknown>) => promise)
    wrapper = shallowMount(DnDZone)
    expect(mocks.dropCallbacks).toHaveLength(2)
  })

  afterEach(() => {
    wrapper.unmount()
  })

  it('waits for a memory-backed copy before queueing a dropped file', async () => {
    const droppedFile = new File([new Uint8Array([1, 2, 3])], 'queued.torrent', {
      type: 'application/x-bittorrent',
      lastModified: 1_234,
    })
    let finishReading: ((contents: ArrayBuffer) => void) | undefined
    const read = vi.spyOn(droppedFile, 'arrayBuffer').mockReturnValue(
      new Promise(resolve => {
        finishReading = resolve
      })
    )

    mocks.dropCallbacks[0]([droppedFile], createDropEvent())

    expect(read).toHaveBeenCalledOnce()
    expect(mocks.pushTorrentToQueue).not.toHaveBeenCalled()
    expect(mocks.createDialog).not.toHaveBeenCalled()

    finishReading?.(new Uint8Array([1, 2, 3]).buffer)
    await flushPromises()

    expect(mocks.pushTorrentToQueue).toHaveBeenCalledOnce()
    const queuedFile = mocks.pushTorrentToQueue.mock.calls[0][0] as File
    expect(queuedFile).not.toBe(droppedFile)
    expect(queuedFile.name).toBe(droppedFile.name)
    expect(queuedFile.type).toBe(droppedFile.type)
    expect(queuedFile.lastModified).toBe(droppedFile.lastModified)
    expect(new Uint8Array(await queuedFile.arrayBuffer())).toEqual(new Uint8Array([1, 2, 3]))
    expect(mocks.createDialog).toHaveBeenCalledOnce()
  })

  it('uploads a memory-backed copy through the instant drop zone', async () => {
    const droppedFile = new File([new Uint8Array([4, 5])], 'instant.torrent', {
      type: 'application/x-bittorrent',
      lastModified: 5_678,
    })

    mocks.dropCallbacks[1]([droppedFile], createDropEvent())
    await flushPromises()

    expect(mocks.addTorrents).toHaveBeenCalledOnce()
    const uploadedFiles = mocks.addTorrents.mock.calls[0][0] as File[]
    expect(uploadedFiles).toHaveLength(1)
    expect(uploadedFiles[0]).not.toBe(droppedFile)
    expect(uploadedFiles[0].name).toBe(droppedFile.name)
    expect(new Uint8Array(await uploadedFiles[0].arrayBuffer())).toEqual(new Uint8Array([4, 5]))
    expect(mocks.toastPromise).toHaveBeenCalledOnce()
  })

  it.each([
    ['queue', 0],
    ['instant', 1],
  ] as const)('performs no partial %s action when a dropped file cannot be copied', async (_zone, callbackIndex) => {
    const readable = new File([new Uint8Array([1])], 'readable.torrent')
    const unreadable = new File([new Uint8Array([2])], 'unreadable.torrent')
    vi.spyOn(unreadable, 'arrayBuffer').mockRejectedValue(new Error('File access expired'))

    mocks.dropCallbacks[callbackIndex]([readable, unreadable], createDropEvent('magnet:?xt=urn:btih:test'))
    await flushPromises()

    expect(mocks.toastError).toHaveBeenCalledWith('toast.add.error', { autoClose: 1500 })
    expect(mocks.pushTorrentToQueue).not.toHaveBeenCalled()
    expect(mocks.createDialog).not.toHaveBeenCalled()
    expect(mocks.addTorrents).not.toHaveBeenCalled()
    expect(mocks.toastPromise).not.toHaveBeenCalled()
  })

  it.each([
    ['queue', 0],
    ['instant', 1],
  ] as const)('does nothing for a %s drop with no recognized torrent or link', async (_zone, callbackIndex) => {
    const ignoredFile = new File([new Uint8Array([1])], 'notes.txt', { type: 'text/plain' })
    const read = vi.spyOn(ignoredFile, 'arrayBuffer')

    mocks.dropCallbacks[callbackIndex]([ignoredFile], createDropEvent('not a torrent link'))
    await flushPromises()

    expect(read).not.toHaveBeenCalled()
    expect(mocks.pushTorrentToQueue).not.toHaveBeenCalled()
    expect(mocks.createDialog).not.toHaveBeenCalled()
    expect(mocks.addTorrents).not.toHaveBeenCalled()
    expect(mocks.toastError).not.toHaveBeenCalled()
    expect(mocks.toastPromise).not.toHaveBeenCalled()
  })

  it('keeps a links-only queue drop unchanged', async () => {
    const magnet = 'magnet:?xt=urn:btih:test'

    mocks.dropCallbacks[0](null, createDropEvent(magnet))
    await flushPromises()

    expect(mocks.pushTorrentToQueue).toHaveBeenCalledOnce()
    expect(mocks.pushTorrentToQueue.mock.calls[0][0]).toBe(magnet)
    expect(mocks.createDialog).toHaveBeenCalledOnce()
    expect(mocks.addTorrents).not.toHaveBeenCalled()
  })

  it('keeps a links-only instant drop unchanged', async () => {
    const magnet = 'magnet:?xt=urn:btih:test'

    mocks.dropCallbacks[1](null, createDropEvent(magnet))
    await flushPromises()

    expect(mocks.addTorrents).toHaveBeenCalledWith([], [magnet])
    expect(mocks.toastPromise).toHaveBeenCalledOnce()
    expect(mocks.pushTorrentToQueue).not.toHaveBeenCalled()
  })
})
