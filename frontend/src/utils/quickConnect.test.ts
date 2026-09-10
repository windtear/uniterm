import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'

// identityStore (imported transitively by formatConnSubtitle) pulls in the
// Wails bindings and event runtime; stub both so the import is side-effect safe.
vi.mock('@wailsio/runtime', () => ({
  Events: { On: vi.fn(() => () => {}), Off: vi.fn() },
}))
vi.mock('../../bindings/github.com/ys-ll/uniterm/app', () => ({
  LoadIdentities: vi.fn(async () => ({ identities: [] })),
  SaveIdentities: vi.fn(async () => {}),
}))

import { formatConnSubtitle } from './quickConnect'
import { useIdentityStore } from '../stores/identityStore'
import type { ConnectionConfig } from '../types/session'

describe('formatConnSubtitle', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('shows user@host for a plain password connection', () => {
    const cfg = { type: 'ssh', host: 'srv1', port: 22, user: 'alice', authType: 'password' } as ConnectionConfig
    expect(formatConnSubtitle(cfg)).toBe('ssh alice@srv1')
  })

  it('resolves the username from the referenced identity', () => {
    const identities = useIdentityStore()
    identities.identities = [{ id: 'id-1', name: 'work', username: 'bob', authType: 'key' }]

    const cfg = { type: 'ssh', host: 'srv1', port: 22, user: '', authType: 'identity', identityId: 'id-1' } as ConnectionConfig
    expect(formatConnSubtitle(cfg)).toBe('ssh bob@srv1')
  })

  it('falls back to host-only when the identity no longer exists', () => {
    const cfg = { type: 'ssh', host: 'srv1', port: 22, user: '', authType: 'identity', identityId: 'gone' } as ConnectionConfig
    expect(formatConnSubtitle(cfg)).toBe('ssh srv1')
  })
})
