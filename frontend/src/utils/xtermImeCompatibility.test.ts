import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { installImeCompatibilityPatch } from './xtermImeCompatibility'

interface FakeCore {
  _inputEvent: (this: Record<string, unknown>, ev: InputEvent) => boolean
  _keyDownSeen: boolean
  _compositionHelper: Record<string, unknown>
  textarea: {
    listeners: Map<string, EventListener>
    addEventListener: (type: string, fn: EventListener) => void
    removeEventListener: (type: string, fn: EventListener) => void
  }
}

function makeFakeCore(inputEventResult: boolean = true) {
  const calls: { keyDownSeenAtCall: boolean | undefined }[] = []
  const core: FakeCore = {
    _inputEvent: function (this: Record<string, unknown>, _ev: InputEvent) {
      calls.push({ keyDownSeenAtCall: this._keyDownSeen as boolean | undefined })
      return inputEventResult
    },
    _keyDownSeen: true,
    _compositionHelper: { _isComposing: false, _isSendingComposition: false },
    textarea: {
      listeners: new Map(),
      addEventListener(type: string, fn: EventListener) {
        core.textarea.listeners.set(type, fn)
      },
      removeEventListener(type: string, fn: EventListener) {
        if (core.textarea.listeners.get(type) === fn) core.textarea.listeners.delete(type)
      },
    },
  }
  return { core, calls }
}

function fakeTerminal(core: FakeCore) {
  return { _core: core } as unknown as import('@xterm/xterm').Terminal
}

function keydown(keyCode: number) {
  return { keyCode } as KeyboardEvent
}

function insertText(data: string, opts: { isComposing?: boolean } = {}) {
  return {
    inputType: 'insertText',
    data,
    isComposing: opts.isComposing ?? false,
  } as unknown as InputEvent
}

beforeEach(() => {
  vi.stubGlobal('navigator', { userAgent: 'Macintosh; Intel Mac OS X 10_15_7' })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('installImeCompatibilityPatch', () => {
  it('is a no-op on non-mac platforms', () => {
    vi.stubGlobal('navigator', { userAgent: 'Windows NT 10.0' })
    const { core, calls } = makeFakeCore()
    const disposable = installImeCompatibilityPatch(fakeTerminal(core), () => true)
    expect(core._inputEvent).toBe(core._inputEvent)
    expect(core.textarea.listeners.size).toBe(0)
    disposable.dispose()
    expect(calls).toHaveLength(0)
  })

  it('does not patch when xterm internals are missing', () => {
    const disposable = installImeCompatibilityPatch({ _core: {} } as never, () => true)
    expect(disposable.dispose).toBeTypeOf('function')
    disposable.dispose()
  })

  it('forces direct delivery for a single printable char after a 229 keydown', () => {
    const { core, calls } = makeFakeCore()
    installImeCompatibilityPatch(fakeTerminal(core), () => true)

    core.textarea.listeners.get('keydown')!(keydown(229))
    core._inputEvent.call(core, insertText('a'))

    // Original handler must see _keyDownSeen cleared (direct path)...
    expect(calls).toEqual([{ keyDownSeenAtCall: false }])
    // ...and the flag restored afterwards so xterm bookkeeping stays intact.
    expect(core._keyDownSeen).toBe(true)
  })

  it('leaves input untouched without a preceding 229 keydown', () => {
    const { core, calls } = makeFakeCore()
    installImeCompatibilityPatch(fakeTerminal(core), () => true)

    core.textarea.listeners.get('keydown')!(keydown(65))
    core._inputEvent.call(core, insertText('a'))

    expect(calls).toEqual([{ keyDownSeenAtCall: true }])
  })

  it('only forces once per keydown', () => {
    const { core, calls } = makeFakeCore()
    installImeCompatibilityPatch(fakeTerminal(core), () => true)

    core.textarea.listeners.get('keydown')!(keydown(229))
    core._inputEvent.call(core, insertText('a'))
    core._inputEvent.call(core, insertText('b'))

    expect(calls).toEqual([
      { keyDownSeenAtCall: false },
      { keyDownSeenAtCall: true },
    ])
  })

  it('leaves real composition untouched', () => {
    const { core, calls } = makeFakeCore()
    installImeCompatibilityPatch(fakeTerminal(core), () => true)

    core.textarea.listeners.get('keydown')!(keydown(229))
    core._inputEvent.call(core, insertText('a', { isComposing: true }))
    expect(calls).toEqual([{ keyDownSeenAtCall: true }])

    core._compositionHelper._isComposing = true
    core.textarea.listeners.get('keydown')!(keydown(229))
    core._inputEvent.call(core, insertText('a'))
    expect(calls).toEqual([
      { keyDownSeenAtCall: true },
      { keyDownSeenAtCall: true },
    ])
  })

  it('leaves multi-character and non-insertText input untouched', () => {
    const { core, calls } = makeFakeCore()
    installImeCompatibilityPatch(fakeTerminal(core), () => true)

    core.textarea.listeners.get('keydown')!(keydown(229))
    core._inputEvent.call(core, insertText('ab'))
    expect(calls).toEqual([{ keyDownSeenAtCall: true }])

    core.textarea.listeners.get('keydown')!(keydown(229))
    core._inputEvent.call(core, { inputType: 'deleteContentBackward' } as unknown as InputEvent)
    expect(calls).toEqual([
      { keyDownSeenAtCall: true },
      { keyDownSeenAtCall: true },
    ])
  })

  it('is inert when disabled', () => {
    const { core, calls } = makeFakeCore()
    let enabled = true
    installImeCompatibilityPatch(fakeTerminal(core), () => enabled)

    core.textarea.listeners.get('keydown')!(keydown(229))
    core._inputEvent.call(core, insertText('a'))
    expect(calls).toEqual([{ keyDownSeenAtCall: false }])

    enabled = false
    core.textarea.listeners.get('keydown')!(keydown(229))
    core._inputEvent.call(core, insertText('b'))
    expect(calls).toEqual([
      { keyDownSeenAtCall: false },
      { keyDownSeenAtCall: true },
    ])
  })

  it('dispose restores the original handler and removes the keydown listener', () => {
    const { core } = makeFakeCore()
    const original = core._inputEvent
    const disposable = installImeCompatibilityPatch(fakeTerminal(core), () => true)

    expect(core._inputEvent).not.toBe(original)
    expect(core.textarea.listeners.has('keydown')).toBe(true)

    disposable.dispose()
    expect(core._inputEvent).toBe(original)
    expect(core.textarea.listeners.size).toBe(0)
  })
})
