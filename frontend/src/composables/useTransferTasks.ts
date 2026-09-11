import { onUnmounted } from 'vue'
import { Events } from '@wailsio/runtime'
import type { TransferTaskUI } from '../stores/panelStore'

function formatSpeed(bytesPerSec: number): string {
  if (bytesPerSec < 1024) return Math.round(bytesPerSec) + ' B/s'
  if (bytesPerSec < 1024 * 1024) return (bytesPerSec / 1024).toFixed(1) + ' KB/s'
  return (bytesPerSec / (1024 * 1024)).toFixed(1) + ' MB/s'
}

function formatETA(seconds: number): string {
  if (seconds < 1) return ''
  if (seconds < 60) return Math.round(seconds) + 's'
  const m = Math.floor(seconds / 60)
  if (m < 60) return m + 'm'
  const h = Math.floor(m / 60)
  return h + 'h' + (m % 60 ? (m % 60) + 'm' : '')
}

/**
 * Shared transfer-task bookkeeping for a file tab / file sidebar. Subscribes to
 * the backend's `sftp:transfer` events scoped to a session and maintains one
 * reactive task list: start pushes a task, progress updates it, and completion
 * marks its status. Finished tasks are KEPT until the user clears them (the file
 * sidebar's behavior), so the list never self-prunes mid-view.
 */
export function useTransferTaskEvents(
  getTasks: () => TransferTaskUI[],
  getSessionId: () => string | undefined,
  onDone: (status: string, type: string) => void,
) {
  let unsub: (() => void) | null = null

  function unbind() {
    try { unsub?.(); unsub = null } catch { /* ignore */ }
  }

  function bind() {
    unbind()
    unsub = Events.On('sftp:transfer', (ev) => {
      const msg = ev?.data as any
      if (!msg || msg.type !== 'sftp:transfer' || msg.sessionId !== getSessionId()) return
      const tasks = getTasks()

      if (msg.event === 'start') {
        const existing = tasks.find(t => t.id === msg.taskId)
        if (existing) {
          existing.status = 'running'
          existing.speed = ''
          existing.eta = ''
          existing.lastBytes = 0
          existing.lastTime = Date.now()
        } else {
          tasks.push({
            id: msg.taskId,
            type: msg.tfType,
            name: msg.name,
            localPath: msg.localPath || '',
            remotePath: msg.remotePath || '',
            percentage: 0,
            speed: '',
            eta: '',
            status: 'running',
            lastBytes: 0,
            lastTime: Date.now(),
            total: msg.total || 0,
            fileCount: msg.fileCount || 0,
            completedFiles: 0,
            currentFile: '',
            files: [],
            failedFiles: [],
          })
          while (tasks.length > 80) tasks.shift()
        }
      } else if (msg.event === 'progress') {
        const existing = tasks.find(t => t.id === msg.taskId)
        if (existing) {
          existing.total = msg.total || existing.total
          existing.percentage = existing.total > 0 ? Math.round((msg.progress / existing.total) * 100) : 0
          const now = Date.now()
          const elapsed = (now - existing.lastTime) / 1000
          if (elapsed >= 0.5) {
            const bytesSince = msg.progress - existing.lastBytes
            const bytesPerSec = bytesSince / elapsed
            existing.speed = formatSpeed(bytesPerSec)
            if (bytesPerSec > 0 && existing.total > 0) {
              existing.eta = formatETA((existing.total - msg.progress) / bytesPerSec)
            }
            existing.lastBytes = msg.progress
            existing.lastTime = now
          }
        }
      } else if (msg.event === 'file-start') {
        const t = tasks.find(t => t.id === msg.taskId)
        if (t) {
          t.currentFile = msg.name || msg.file
          t.files.push({ path: msg.file, status: 'running' })
        }
      } else if (msg.event === 'file-done') {
        const t = tasks.find(t => t.id === msg.taskId)
        if (t) {
          const f = t.files.find((f: any) => f.path === msg.file)
          if (f) f.status = 'done'
          else t.files.push({ path: msg.file, status: 'done' })
          t.completedFiles = msg.completedFiles ?? t.completedFiles
          t.fileCount = msg.fileCount || t.fileCount
        }
      } else if (msg.event === 'file-failed') {
        const t = tasks.find(t => t.id === msg.taskId)
        if (t) {
          const f = t.files.find((f: any) => f.path === msg.file)
          if (f) f.status = 'failed'
          else t.files.push({ path: msg.file, status: 'failed' })
          t.failedFiles.push({ path: msg.file, error: msg.error })
        }
      } else if (msg.event === 'paused') {
        const t = tasks.find(t => t.id === msg.taskId)
        if (t) t.status = 'paused'
      } else if (msg.event === 'resumed') {
        const t = tasks.find(t => t.id === msg.taskId)
        if (t) {
          t.status = 'running'
          // Reset the speed baseline so the pause duration doesn't skew the
          // next speed/eta sample.
          t.lastTime = Date.now()
        }
      } else if (msg.event === 'complete') {
        const existing = tasks.find(t => t.id === msg.taskId)
        if (existing) {
          const st = msg.status as string
          existing.status = st === 'done' ? 'done' : st === 'cancelled' ? 'cancelled' : st === 'paused' ? 'paused' : 'error'
          existing.percentage = existing.status === 'done' ? 100 : existing.percentage
          if (msg.failedFiles) existing.failedFiles = msg.failedFiles
          onDone(existing.status, existing.type)
          // Finished tasks stay listed until the user clears them.
        }
      }
    })
  }

  onUnmounted(unbind)

  return { bind, unbind }
}

/**
 * Keep a finished-transfers counter for cheap UI state (e.g. enabling the
 * "clear completed" button) without recomputing by hand everywhere.
 */
export function countFinishedTasks(tasks: TransferTaskUI[]): number {
  return tasks.filter(t => t.status === 'done' || t.status === 'error' || t.status === 'cancelled').length
}

/**
 * Relative paths of files already completed — the retry skip list. Passed to
 * SftpRetryTransfer so the backend only re-transfers what failed or never ran.
 */
export function buildSkipList(task: TransferTaskUI): string[] {
  return task.files.filter(f => f.status === 'done').map(f => f.path)
}
