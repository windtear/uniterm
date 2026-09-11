import type { KeyboardSettings, KeyBinding, ShortcutAction } from '../types/settings'

type ActionHandlers = Record<ShortcutAction, () => void>

/**
 * Render a KeyBinding as a human-readable combo, e.g. Ctrl+Shift+C.
 * Shared by the settings UI and the terminal context-menu shortcut hints so
 * both show the same format. Ctrl-based bindings are mirrored to Command by
 * loadKeybindings on macOS, so render that portable primary modifier as Cmd.
 */
export function formatKeyBinding(b: KeyBinding, isMac: boolean): string {
  if (!b) return ''
  const parts: string[] = []
  if (b.ctrl) parts.push(isMac ? 'Cmd' : 'Ctrl')
  if (b.meta && !b.ctrl) parts.push(isMac ? 'Cmd' : 'Meta')
  if (b.shift) parts.push('Shift')
  if (b.alt) parts.push('Alt')
  parts.push(b.key)
  return parts.join('+')
}

export function migrateLegacyQuickCommandsBinding(
  binding: KeyBinding | undefined,
  isMac: boolean,
): KeyBinding | undefined {
  if (isMac || !binding?.meta || binding.ctrl || binding.shift || binding.alt
    || binding.key.toLowerCase() !== 'k') {
    return binding
  }
  return { ctrl: true, meta: false, shift: false, alt: false, key: 'k' }
}

function bindingKey(b: KeyBinding): string {
  if (!b.key) return ''
  let k = ''
  if (b.ctrl) k += 'ctrl+'
  if (b.meta) k += 'meta+'
  if (b.shift) k += 'shift+'
  if (b.alt) k += 'alt+'
  k += b.key.toLowerCase()
  return k
}

function normalize(e: KeyboardEvent): string {
  const parts: string[] = []
  if (e.ctrlKey) parts.push('ctrl')
  if (e.metaKey) parts.push('meta')
  if (e.shiftKey) parts.push('shift')
  if (e.altKey) parts.push('alt')
  parts.push(e.key.toLowerCase())
  return parts.join('+')
}

export type PlatformDigitShortcut =
  | { action: 'tab'; index: number }
  | { action: 'workspace'; index: number }

// Resolve only exact platform digit combinations. In particular, Alt+N and
// Ctrl+N are mutually exclusive on Windows and must never select the same UI.
export function resolvePlatformDigitShortcut(
  e: Pick<KeyboardEvent, 'code' | 'ctrlKey' | 'metaKey' | 'shiftKey' | 'altKey'>,
  isMac: boolean,
): PlatformDigitShortcut | null {
  const match = e.code.match(/^Digit([0-9])$/)
  if (!match || e.shiftKey) return null
  // Match the conventional terminal shortcut layout: 1…9 select the first
  // nine entries and 0 selects the tenth.
  const digit = Number(match[1])
  const index = digit === 0 ? 9 : digit - 1
  if (e.altKey && !e.metaKey && !e.ctrlKey) {
    return { action: 'workspace', index }
  }
  const tabModifier = !e.altKey && (
    (isMac && e.metaKey && !e.ctrlKey) ||
    (!isMac && e.ctrlKey && !e.metaKey)
  )
  return tabModifier ? { action: 'tab', index } : null
}

// Module-level state: key combo → action handler
const shortcutMap = new Map<string, () => void>()
// Terminal-scoped shortcuts: only fire while a terminal session is focused
// (handled by onTerminalKey), never from the global capture listener, so they
// don't hijack copy/paste while the user is typing in another input.
const terminalShortcutMap = new Map<string, () => void>()
// Reverse lookup: action → key combo (for display / dedup)
const actionKeyMap = new Map<ShortcutAction, string>()

// Actions that should only take effect while a terminal session is focused.
const TERMINAL_SCOPED_ACTIONS: ShortcutAction[] = ['copy', 'paste']

export function loadKeybindings(
  bindings: KeyboardSettings,
  handlers: ActionHandlers,
  isMac = false,
) {
  shortcutMap.clear()
  terminalShortcutMap.clear()
  actionKeyMap.clear()
  for (const [action, b] of Object.entries(bindings) as [ShortcutAction, KeyBinding][]) {
    const key = bindingKey(b)
    if (!key) continue
    const handler = handlers[action]
    if (handler) {
      const target = TERMINAL_SCOPED_ACTIONS.includes(action) ? terminalShortcutMap : shortcutMap
      target.set(key, handler)
      if (isMac && !b.meta && b.ctrl) {
        target.set(key.replace(/^ctrl\+/, 'meta+'), handler)
      }
      actionKeyMap.set(action, key)
    }
  }
}

export function getActionKey(action: ShortcutAction): string {
  return actionKeyMap.get(action) || ''
}

function fire(e: KeyboardEvent, normalized: string, map: Map<string, () => void>): boolean {
  const handler = map.get(normalized)
  if (!handler) return false
  e.preventDefault()
  e.stopPropagation()
  handler()
  return true
}

export function onGlobalKeydown(e: KeyboardEvent) {
  fire(e, normalize(e), shortcutMap)
}

export function onTerminalKey(e: KeyboardEvent): boolean {
  const normalized = normalize(e)
  if (fire(e, normalized, shortcutMap)) return false
  if (fire(e, normalized, terminalShortcutMap)) return false
  return true
}

let registered = false

export function installGlobalListener() {
  if (registered) return
  registered = true
  window.addEventListener('keydown', onGlobalKeydown, true)
}

export function uninstallGlobalListener() {
  registered = false
  window.removeEventListener('keydown', onGlobalKeydown, true)
}
