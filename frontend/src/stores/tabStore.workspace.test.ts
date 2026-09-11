import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useTabStore } from './tabStore'

describe('tabStore.addNewPanelToWorkspace', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('adds a new panel beside the requested panel and activates it', () => {
    const store = useTabStore()
    const workspace = store.createWorkspaceTab('Workspace', ['panel-a'], {
      root: { type: 'leaf', panelId: 'panel-a' },
    })

    expect(store.addNewPanelToWorkspace(workspace.id, 'panel-b', 'panel-a')).toBe(true)
    expect(workspace.panelIds).toEqual(['panel-a', 'panel-b'])
    expect(workspace.activePanelId).toBe('panel-b')
    expect(workspace.layout.root).toEqual({
      type: 'split',
      direction: 'horizontal',
      sizes: [0.5, 0.5],
      children: [
        { type: 'leaf', panelId: 'panel-a' },
        { type: 'leaf', panelId: 'panel-b' },
      ],
    })
  })

  it('returns false when the target workspace no longer exists', () => {
    const store = useTabStore()
    expect(store.addNewPanelToWorkspace('missing', 'panel-b')).toBe(false)
  })
})
