import type { KeyboardSettings, KeyBinding, ShortcutAction } from '../types/settings'

type ActionHandlers = Record<ShortcutAction, () => void>

/**
 * Render a KeyBinding as a human-readable combo, e.g. Ctrl+Shift+C.
 * Shared by the settings UI and the terminal context-menu shortcut hints so
 * both show the same format (Cmd on macOS, Meta elsewhere).
 */
export function formatKeyBinding(b: KeyBinding, isMac: boolean): string {
  if (!b) return ''
  const parts: string[] = []
  if (b.ctrl) parts.push('Ctrl')
  if (b.meta) parts.push(isMac ? 'Cmd' : 'Meta')
  if (b.shift) parts.push('Shift')
  if (b.alt) parts.push('Alt')
  parts.push(b.key)
  return parts.join('+')
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

// Module-level state: key combo → action handler
const shortcutMap = new Map<string, () => void>()
// Terminal-scoped shortcuts: only fire while a terminal session is focused
// (handled by onTerminalKey), never from the global capture listener, so they
// don't hijack copy/paste while the user is typing in another input.
const terminalShortcutMap = new Map<string, () => void>()
// Reverse lookup: action → key combo (for display / dedup)
const actionKeyMap = new Map<ShortcutAction, string>()

// "Modifier + digit" tab switching: the configured combo held together with
// 1-9 (or 0 = tenth tab) jumps to that tab. Not a per-action binding — the
// user configures only the modifier (see KeyboardSettings.tabSwitchModifier),
// and the digit selects the tab. A null modifier disables the feature.
let tabSwitchModifier: KeyBinding | null = null
let tabSwitchHandler: ((index: number) => void) | null = null

// Actions that should only take effect while a terminal session is focused.
const TERMINAL_SCOPED_ACTIONS: ShortcutAction[] = ['copy', 'paste']

export function loadKeybindings(
  bindings: KeyboardSettings,
  handlers: ActionHandlers,
  onTabSwitch?: (index: number) => void,
) {
  shortcutMap.clear()
  terminalShortcutMap.clear()
  actionKeyMap.clear()
  for (const [action, b] of Object.entries(bindings) as [ShortcutAction, KeyBinding][]) {
    // Handled below, not through the per-action maps.
    if (action === ('tabSwitchModifier' as ShortcutAction)) continue
    const key = bindingKey(b)
    if (!key) continue
    const handler = handlers[action]
    if (handler) {
      const target = TERMINAL_SCOPED_ACTIONS.includes(action) ? terminalShortcutMap : shortcutMap
      target.set(key, handler)
      if (!b.meta && b.ctrl) {
        target.set(key.replace(/^ctrl\+/, 'meta+'), handler)
      }
      actionKeyMap.set(action, key)
    }
  }
  tabSwitchHandler = onTabSwitch ?? null
  const mod = bindings.tabSwitchModifier
  // Require at least one modifier so a cleared binding can't swallow digits.
  tabSwitchModifier = mod && (mod.ctrl || mod.meta || mod.shift || mod.alt) ? mod : null
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

// Exact-match the event's modifier flags against the configured combo (an
// accidental extra modifier must not trigger a jump), then map the digit to a
// 1-based tab index: 1-9 → tabs 1-9, 0 → tab 10.
function tryTabSwitch(e: KeyboardEvent): boolean {
  if (!tabSwitchModifier || !tabSwitchHandler) return false
  // Composition keystrokes (CJK IME candidate picking) must never jump tabs —
  // mirrors the global IME guard in main.ts (keyCode 229 = WKWebView phantom).
  if (e.isComposing || e.keyCode === 229) return false
  const m = tabSwitchModifier
  if (e.ctrlKey !== !!m.ctrl || e.metaKey !== !!m.meta
    || e.shiftKey !== !!m.shift || e.altKey !== !!m.alt) return false
  if (!/^[0-9]$/.test(e.key)) return false
  tabSwitchHandler(e.key === '0' ? 10 : Number(e.key))
  e.preventDefault()
  e.stopPropagation()
  return true
}

export function onGlobalKeydown(e: KeyboardEvent) {
  if (fire(e, normalize(e), shortcutMap)) return
  tryTabSwitch(e)
}

export function onTerminalKey(e: KeyboardEvent): boolean {
  const normalized = normalize(e)
  if (fire(e, normalized, shortcutMap)) return false
  if (fire(e, normalized, terminalShortcutMap)) return false
  if (tryTabSwitch(e)) return false
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
