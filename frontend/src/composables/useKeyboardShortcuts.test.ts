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

describe('useKeyboardShortcuts — modifier+digit tab switching', () => {
  const load = (kb: Particle<KeyboardSettings>, onTabSwitch: (i: number) => void) =>
    loadKeybindings(kb as KeyboardSettings, {} as any, onTabSwitch)

  it('Alt+1 … Alt+9 jump to tabs 1-9, Alt+0 jumps to the tenth tab', () => {
    const onTabSwitch = vi.fn()
    load({ tabSwitchModifier: { ctrl: false, meta: false, shift: false, alt: true, key: '' } }, onTabSwitch)
    onGlobalKeydown(fakeKey({ altKey: true, key: '1' }))
    onGlobalKeydown(fakeKey({ altKey: true, key: '9' }))
    onGlobalKeydown(fakeKey({ altKey: true, key: '0' }))
    expect(onTabSwitch.mock.calls.map(c => c[0])).toEqual([1, 9, 10])
  })

  it('ignores digits when the active modifiers do not match exactly', () => {
    const onTabSwitch = vi.fn()
    load({ tabSwitchModifier: { ctrl: false, meta: false, shift: false, alt: true, key: '' } }, onTabSwitch)
    onGlobalKeydown(fakeKey({ key: '1' })) // bare digit must reach the terminal
    onGlobalKeydown(fakeKey({ ctrlKey: true, altKey: true, key: '1' })) // extra modifier
    onGlobalKeydown(fakeKey({ shiftKey: true, altKey: true, key: '1' }))
    onGlobalKeydown(fakeKey({ altKey: true, key: 'F1' })) // non-digit keys stay unharmed
    expect(onTabSwitch).not.toHaveBeenCalled()
  })

  it('supports a different modifier combo (Ctrl+Shift+digit)', () => {
    const onTabSwitch = vi.fn()
    load({ tabSwitchModifier: { ctrl: true, meta: false, shift: true, alt: false, key: '' } }, onTabSwitch)
    onGlobalKeydown(fakeKey({ ctrlKey: true, shiftKey: true, key: '3' }))
    expect(onTabSwitch).toHaveBeenCalledWith(3)
  })

  it('is disabled when no modifier is configured (cleared binding)', () => {
    const onTabSwitch = vi.fn()
    load({ tabSwitchModifier: { ctrl: false, meta: false, shift: false, alt: false, key: '' } }, onTabSwitch)
    onGlobalKeydown(fakeKey({ key: '1' }))
    expect(onTabSwitch).not.toHaveBeenCalled()
  })

  it('ignores IME composition keystrokes (candidate picking)', () => {
    const onTabSwitch = vi.fn()
    load({ tabSwitchModifier: { ctrl: false, meta: false, shift: false, alt: true, key: '' } }, onTabSwitch)
    onGlobalKeydown(fakeKey({ altKey: true, key: '2', isComposing: true }))
    onGlobalKeydown(fakeKey({ altKey: true, key: '2', keyCode: 229 }))
    expect(onTabSwitch).not.toHaveBeenCalled()
  })

  it('explicit per-action bindings take precedence over the digit family', () => {
    const onTabSwitch = vi.fn()
    const nextTab = vi.fn()
    loadKeybindings(
      {
        nextTab: { ctrl: false, shift: false, alt: true, key: '2' },
        tabSwitchModifier: { ctrl: false, meta: false, shift: false, alt: true, key: '' },
      } as KeyboardSettings,
      { nextTab } as any,
      onTabSwitch,
    )
    onGlobalKeydown(fakeKey({ altKey: true, key: '2' }))
    expect(nextTab).toHaveBeenCalledTimes(1)
    expect(onTabSwitch).not.toHaveBeenCalled()
    onGlobalKeydown(fakeKey({ altKey: true, key: '3' }))
    expect(onTabSwitch).toHaveBeenCalledWith(3)
  })
})

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
