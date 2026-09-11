import { describe, it, expect, vi } from 'vitest'
import { loadKeybindings, onGlobalKeydown } from './useKeyboardShortcuts'
import type { KeyboardSettings } from '../types/settings'

function fakeKey(partial: Partial<{ ctrlKey: boolean; metaKey: boolean; shiftKey: boolean; altKey: boolean; key: string; isComposing: boolean; keyCode: number }>): KeyboardEvent {
  return {
    ctrlKey: false, metaKey: false, shiftKey: false, altKey: false, key: '',
    isComposing: false, keyCode: 0,
    preventDefault() {}, stopPropagation() {},
    ...partial,
  } as any
}

describe('useKeyboardShortcuts — font zoom bindings', () => {
  it('fires the handler on Ctrl+= (zoom in)', () => {
    const zoomFontIn = vi.fn()
    loadKeybindings(
      { zoomFontIn: { ctrl: true, shift: false, alt: false, key: '=' } } as Particle<KeyboardSettings>,
      { zoomFontIn } as any,
    )
    onGlobalKeydown(fakeKey({ ctrlKey: true, key: '=' }))
    expect(zoomFontIn).toHaveBeenCalledTimes(1)
  })

  it('mirrors Ctrl+= to Meta+= (issue #614 ⌘ zoom)', () => {
    const zoomFontIn = vi.fn()
    loadKeybindings(
      { zoomFontIn: { ctrl: true, shift: false, alt: false, key: '=' } } as Particle<KeyboardSettings>,
      { zoomFontIn } as any,
    )
    onGlobalKeydown(fakeKey({ metaKey: true, key: '=' }))
    expect(zoomFontIn).toHaveBeenCalledTimes(1)
  })

  it('fires the handler on Ctrl+- (zoom out) but not on the raw "=" key alone', () => {
    const zoomFontOut = vi.fn()
    loadKeybindings(
      { zoomFontOut: { ctrl: true, shift: false, alt: false, key: '-' } } as Particle<KeyboardSettings>,
      { zoomFontOut } as any,
    )
    onGlobalKeydown(fakeKey({ ctrlKey: true, key: '-' }))
    expect(zoomFontOut).toHaveBeenCalledTimes(1)
    onGlobalKeydown(fakeKey({ key: '=' }))
    expect(zoomFontOut).toHaveBeenCalledTimes(1) // no modifier → unharmed
  })

  it('does not fire when the active modifiers do not match the binding', () => {
    const zoomFontIn = vi.fn()
    loadKeybindings(
      { zoomFontIn: { ctrl: true, shift: false, alt: false, key: '=' } } as Particle<KeyboardSettings>,
      { zoomFontIn } as any,
    )
    onGlobalKeydown(fakeKey({ ctrlKey: true, shiftKey: true, key: '=' }))
    expect(zoomFontIn).not.toHaveBeenCalled()
  })
})

type Particle<T> = Partial<{ [K in keyof T]?: T[K] }>

describe('useKeyboardShortcuts — quick commands', () => {
  it('fires the quick-command handler on Meta+K only', () => {
    const openQuickCommands = vi.fn()
    loadKeybindings(
      { openQuickCommands: { ctrl: false, meta: true, shift: false, alt: false, key: 'k' } },
      { openQuickCommands } as any,
    )

    onGlobalKeydown(fakeKey({ metaKey: true, key: 'k' }))
    onGlobalKeydown(fakeKey({ ctrlKey: true, key: 'k' }))

    expect(openQuickCommands).toHaveBeenCalledTimes(1)
  })
})
