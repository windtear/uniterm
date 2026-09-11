import { describe, it, expect, vi } from 'vitest'
import { formatKeyBinding, loadKeybindings, migrateLegacyQuickCommandsBinding, onGlobalKeydown, resolvePlatformDigitShortcut } from './useKeyboardShortcuts'
import { DEFAULT_KEYBOARD, type KeyboardSettings } from '../types/settings'

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
      true,
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
  it('uses Ctrl+K on Windows and mirrors it to Command+K on macOS', () => {
    const openQuickCommands = vi.fn()
    loadKeybindings(
      { openQuickCommands: DEFAULT_KEYBOARD.openQuickCommands! },
      { openQuickCommands } as any,
    )

    onGlobalKeydown(fakeKey({ ctrlKey: true, key: 'k' }))
    onGlobalKeydown(fakeKey({ metaKey: true, key: 'k' }))

    expect(openQuickCommands).toHaveBeenCalledTimes(1)
    expect(formatKeyBinding(DEFAULT_KEYBOARD.openQuickCommands!, false)).toBe('Ctrl+k')
    expect(formatKeyBinding(DEFAULT_KEYBOARD.openQuickCommands!, true)).toBe('Cmd+k')
  })

  it('uses Command+K instead of Ctrl+K when loaded for macOS', () => {
    const openQuickCommands = vi.fn()
    loadKeybindings(
      { openQuickCommands: DEFAULT_KEYBOARD.openQuickCommands! },
      { openQuickCommands } as any,
      true,
    )

    onGlobalKeydown(fakeKey({ metaKey: true, key: 'k' }))
    expect(openQuickCommands).toHaveBeenCalledTimes(1)
  })

  it('migrates the legacy Meta+K default to Ctrl+K on Windows', () => {
    const legacy = { ctrl: false, meta: true, shift: false, alt: false, key: 'k' }
    expect(migrateLegacyQuickCommandsBinding(legacy, false))
      .toEqual({ ctrl: true, meta: false, shift: false, alt: false, key: 'k' })
    expect(migrateLegacyQuickCommandsBinding(legacy, true)).toBe(legacy)
  })
})

describe('useKeyboardShortcuts — workspace maximize', () => {
  it('uses Ctrl+Shift+Enter on Windows', () => {
    const toggleWorkspaceMaximize = vi.fn()
    loadKeybindings(
      { toggleWorkspaceMaximize: DEFAULT_KEYBOARD.toggleWorkspaceMaximize! },
      { toggleWorkspaceMaximize } as any,
    )

    onGlobalKeydown(fakeKey({ ctrlKey: true, shiftKey: true, key: 'Enter' }))
    onGlobalKeydown(fakeKey({ metaKey: true, shiftKey: true, key: 'Enter' }))

    expect(toggleWorkspaceMaximize).toHaveBeenCalledTimes(1)
    expect(formatKeyBinding(DEFAULT_KEYBOARD.toggleWorkspaceMaximize!, false))
      .toBe('Ctrl+Shift+enter')
  })

  it('uses Command+Shift+Enter on macOS', () => {
    const toggleWorkspaceMaximize = vi.fn()
    loadKeybindings(
      { toggleWorkspaceMaximize: DEFAULT_KEYBOARD.toggleWorkspaceMaximize! },
      { toggleWorkspaceMaximize } as any,
      true,
    )

    onGlobalKeydown(fakeKey({ metaKey: true, shiftKey: true, key: 'Enter' }))

    expect(toggleWorkspaceMaximize).toHaveBeenCalledTimes(1)
    expect(formatKeyBinding(DEFAULT_KEYBOARD.toggleWorkspaceMaximize!, true))
      .toBe('Cmd+Shift+enter')
  })

  it('honours a user-defined maximize binding', () => {
    const toggleWorkspaceMaximize = vi.fn()
    loadKeybindings(
      { toggleWorkspaceMaximize: { ctrl: false, meta: false, shift: true, alt: true, key: 'm' } },
      { toggleWorkspaceMaximize } as any,
    )

    onGlobalKeydown(fakeKey({ ctrlKey: true, shiftKey: true, key: 'Enter' }))
    onGlobalKeydown(fakeKey({ altKey: true, shiftKey: true, key: 'm' }))

    expect(toggleWorkspaceMaximize).toHaveBeenCalledTimes(1)
  })
})

describe('useKeyboardShortcuts — platform digit shortcuts', () => {
  const digit = (modifiers: Partial<KeyboardEvent>) => ({
    code: 'Digit3', ctrlKey: false, metaKey: false, shiftKey: false, altKey: false,
    ...modifiers,
  } as KeyboardEvent)

  it('keeps Windows Alt+N and Ctrl+N on separate actions', () => {
    expect(resolvePlatformDigitShortcut(digit({ altKey: true }), false))
      .toEqual({ action: 'workspace', index: 2 })
    expect(resolvePlatformDigitShortcut(digit({ ctrlKey: true }), false))
      .toEqual({ action: 'tab', index: 2 })
  })

  it('rejects mixed Alt+Ctrl and maps macOS Command+N to tabs', () => {
    expect(resolvePlatformDigitShortcut(digit({ altKey: true, ctrlKey: true }), false)).toBeNull()
    expect(resolvePlatformDigitShortcut(digit({ metaKey: true }), true))
      .toEqual({ action: 'tab', index: 2 })
  })

  it('maps 0 to the tenth tab and workspace panel', () => {
    const zero = (modifiers: Partial<KeyboardEvent>) => ({
      code: 'Digit0', ctrlKey: false, metaKey: false, shiftKey: false, altKey: false,
      ...modifiers,
    } as KeyboardEvent)

    expect(resolvePlatformDigitShortcut(zero({ ctrlKey: true }), false))
      .toEqual({ action: 'tab', index: 9 })
    expect(resolvePlatformDigitShortcut(zero({ altKey: true }), false))
      .toEqual({ action: 'workspace', index: 9 })
    expect(resolvePlatformDigitShortcut(zero({ metaKey: true }), true))
      .toEqual({ action: 'tab', index: 9 })
    expect(resolvePlatformDigitShortcut(zero({ altKey: true }), true))
      .toEqual({ action: 'workspace', index: 9 })
  })
})
