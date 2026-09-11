import { describe, it, expect, vi, beforeEach } from 'vitest'

const handlers: Record<string, (ev: any) => void> = {}
vi.mock('@wailsio/runtime', () => ({
  Events: {
    On: vi.fn((name: string, cb: (ev: any) => void) => {
      handlers[name] = cb
      return () => { delete handlers[name] }
    }),
  },
}))

import { useTransferTaskEvents } from './useTransferTasks'

function fireTransfer(payload: any) {
  handlers['sftp:transfer']({ data: payload })
}

describe('useTransferTaskEvents', () => {
  beforeEach(() => { for (const k in handlers) delete handlers[k] })

  it('creates a task on start and completes it', () => {
    const tasks: any[] = []
    const onDone = vi.fn()
    const { bind } = useTransferTaskEvents(() => tasks, () => 'sid-1', onDone)
    bind()
    fireTransfer({ sessionId: 'sid-1', type: 'sftp:transfer', taskId: 'dl-1', event: 'start', tfType: 'download', name: 'dir', total: 100 })
    expect(tasks).toHaveLength(1)
    expect(tasks[0]).toMatchObject({ id: 'dl-1', name: 'dir', status: 'running', total: 100 })
    fireTransfer({ sessionId: 'sid-1', type: 'sftp:transfer', taskId: 'dl-1', event: 'progress', progress: 50, total: 100 })
    expect(tasks[0].percentage).toBe(50)
    fireTransfer({ sessionId: 'sid-1', type: 'sftp:transfer', taskId: 'dl-1', event: 'complete', status: 'done' })
    expect(tasks[0].status).toBe('done')
    expect(onDone).toHaveBeenCalledWith('done', 'download')
  })

  it('ignores events from other sessions and non-transfer payloads', () => {
    const tasks: any[] = []
    const { bind } = useTransferTaskEvents(() => tasks, () => 'sid-1', vi.fn())
    bind()
    fireTransfer({ sessionId: 'other', type: 'sftp:transfer', taskId: 'x', event: 'start', tfType: 'download', name: 'x', total: 1 })
    fireTransfer({ sessionId: 'sid-1', type: 'terminal:noise', event: 'start' })
    expect(tasks).toHaveLength(0)
  })

  it('tracks per-file completion for directory transfers', () => {
    const tasks: any[] = []
    const { bind } = useTransferTaskEvents(() => tasks, () => 'sid-1', vi.fn())
    bind()
    fireTransfer({ sessionId: 'sid-1', type: 'sftp:transfer', taskId: 'dl-9', event: 'start', tfType: 'download', name: 'bigdir', total: 300, fileCount: 3 })
    fireTransfer({ sessionId: 'sid-1', type: 'sftp:transfer', taskId: 'dl-9', event: 'file-start', file: 'a.txt', name: 'a.txt' })
    expect(tasks[0].currentFile).toBe('a.txt')
    expect(tasks[0].files).toContainEqual({ path: 'a.txt', status: 'running' })
    fireTransfer({ sessionId: 'sid-1', type: 'sftp:transfer', taskId: 'dl-9', event: 'file-done', file: 'a.txt', completedFiles: 1, fileCount: 3 })
    expect(tasks[0].files[0]).toEqual({ path: 'a.txt', status: 'done' })
    expect(tasks[0].completedFiles).toBe(1)
    fireTransfer({ sessionId: 'sid-1', type: 'sftp:transfer', taskId: 'dl-9', event: 'file-failed', file: 'b.txt', error: 'permission denied' })
    expect(tasks[0].files.find((f: any) => f.path === 'b.txt').status).toBe('failed')
    expect(tasks[0].failedFiles).toContainEqual({ path: 'b.txt', error: 'permission denied' })
  })

  it('maps paused/resumed events onto task status without finishing the task', () => {
    const tasks: any[] = []
    const onDone = vi.fn()
    const { bind } = useTransferTaskEvents(() => tasks, () => 'sid-1', onDone)
    bind()
    fireTransfer({ sessionId: 'sid-1', type: 'sftp:transfer', taskId: 'dl-7', event: 'start', tfType: 'upload', name: 'big.bin', total: 100 })
    fireTransfer({ sessionId: 'sid-1', type: 'sftp:transfer', taskId: 'dl-7', event: 'paused' })
    expect(tasks[0].status).toBe('paused')
    fireTransfer({ sessionId: 'sid-1', type: 'sftp:transfer', taskId: 'dl-7', event: 'resumed' })
    expect(tasks[0].status).toBe('running')
    // Pause/resume is not a completion: the finished callback must not fire.
    expect(onDone).not.toHaveBeenCalled()
  })
})
