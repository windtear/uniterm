import type { Terminal } from '@xterm/xterm'

// IME compatibility patch for xterm.js on macOS (WKWebView).
//
// Root cause: with an IME active, WKWebView reports ordinary keystrokes as
// keydown keyCode=229 ("composition" code) even when no real composition is
// running. xterm records `_keyDownSeen = true` on every keydown, and its
// `_inputEvent` fast path (`insertText` → direct delivery) requires
// `!ev.composed || !_keyDownSeen` — so the character is diverted to
// CompositionHelper's deferred textarea-diff fallback. That fallback races
// under fast typing and drops or duplicates characters (the "must type
// slowly" symptom).
//
// Fix: replace `core._inputEvent` with a wrapper that, when an `insertText`
// event carries a single printable ASCII character right after a
// keyCode-229 keydown while NO composition is actually active, temporarily
// clears `_keyDownSeen` so the character takes the direct delivery path.
// The guard conditions are deliberately conservative — real IME
// composition (pinyin, kana, hangul) is never touched.
//
// The wrapper reads `isEnabled` on every input event, so toggling the
// setting takes effect immediately on already-created terminals.

export interface Disposable {
  dispose(): void
}

interface XtermCompositionHelperInternals {
  _isComposing?: unknown
  _isSendingComposition?: unknown
  isComposing?: unknown
}

interface XtermCoreInternals {
  _inputEvent?: (this: XtermCoreInternals, ev: InputEvent) => boolean
  _keyDownSeen?: boolean
  _compositionHelper?: XtermCompositionHelperInternals
  textarea?: {
    addEventListener: typeof EventTarget.prototype.addEventListener
    removeEventListener: typeof EventTarget.prototype.removeEventListener
  } | null
}

type TerminalWithCore = Terminal & { _core?: XtermCoreInternals }

const PRINTABLE_ASCII = /^[\x20-\x7E]$/

const noopDisposable: Disposable = { dispose() {} }

function isMacPlatform(): boolean {
  return /Mac|iPhone|iPad/.test(navigator.userAgent)
}

function isCompositionActive(helper: XtermCompositionHelperInternals): boolean {
  return (
    helper._isComposing === true ||
    helper._isSendingComposition === true ||
    helper.isComposing === true
  )
}

/**
 * Installs the IME compatibility patch on the given terminal.
 *
 * Only does anything on macOS (the keyCode-229 phantom keydown behavior is
 * WKWebView-specific). The returned Disposable restores the original
 * internals; call it when the terminal is disposed.
 *
 * @param isEnabled called on each input event to decide whether the patch
 *   is active — lets the setting toggle apply without recreating terminals.
 */
export function installImeCompatibilityPatch(
  terminal: Terminal,
  isEnabled: () => boolean,
): Disposable {
  if (!isMacPlatform()) {
    return noopDisposable
  }

  const core = (terminal as TerminalWithCore)._core
  const helper = core?._compositionHelper
  if (
    !core ||
    !helper ||
    typeof core._inputEvent !== 'function' ||
    typeof core._keyDownSeen !== 'boolean' ||
    !core.textarea ||
    typeof core.textarea.addEventListener !== 'function'
  ) {
    // xterm internals changed shape — fail safe by not patching at all.
    return noopDisposable
  }

  // Track whether the most recent keydown was the phantom keyCode 229.
  // Capture phase so it runs before xterm's own textarea keydown listener.
  let latestKeydownWas229 = false
  const onKeyDown = (ev: KeyboardEvent) => {
    latestKeydownWas229 = ev.keyCode === 229
  }
  core.textarea.addEventListener('keydown', onKeyDown, true)

  const originalInputEvent = core._inputEvent
  const patchedInputEvent = function patchedInputEvent(
    this: XtermCoreInternals,
    ev: InputEvent,
  ): boolean {
    const was229 = latestKeydownWas229
    latestKeydownWas229 = false

    if (
      !isEnabled() ||
      ev.inputType !== 'insertText' ||
      !ev.data ||
      !PRINTABLE_ASCII.test(ev.data) ||
      ev.isComposing ||
      isCompositionActive(helper) ||
      !was229 ||
      this._keyDownSeen !== true
    ) {
      return originalInputEvent!.call(this, ev)
    }

    // Force the direct delivery path: clear the poisoned `_keyDownSeen` for
    // the duration of the original handler, then restore it so xterm's own
    // keyup bookkeeping stays consistent.
    const saved = this._keyDownSeen
    this._keyDownSeen = false
    try {
      return originalInputEvent!.call(this, ev)
    } finally {
      this._keyDownSeen = saved
    }
  }

  core._inputEvent = patchedInputEvent

  return {
    dispose() {
      core.textarea?.removeEventListener('keydown', onKeyDown, true)
      if (core._inputEvent === patchedInputEvent) {
        core._inputEvent = originalInputEvent
      }
    },
  }
}
