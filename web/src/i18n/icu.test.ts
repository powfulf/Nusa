import i18next from 'i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ICUFormat from './icu'

/*
 * The check M0 never had.
 *
 * §9 requires ICU MessageFormat: "Indonesian has no plural forms; Arabic has
 * six. The library handles it — do not hand-roll." That was wired in M0 and
 * recorded as done, and it had never formatted a single message, because the
 * package could not construct the formatter and returned the raw string
 * instead of failing.
 *
 * What made that invisible was not the bug. It was that no message in either
 * catalogue had a placeholder, so an unformatted string and a formatted one
 * were the same string. A check that would have caught it needs a message with
 * something in it to format — which is precisely the "prove the premise exists
 * before asserting what follows from it" discipline in §11.
 */

function instanceWith(lng: string, messages: Record<string, string>) {
  const i18n = i18next.createInstance()
  void i18n.use(new ICUFormat()).init({
    lng,
    resources: { [lng]: { translation: messages } },
    interpolation: { escapeValue: false },
  })
  return i18n
}

describe('ICU MessageFormat is actually applied', () => {
  it('substitutes a variable, which an unwired formatter would not', () => {
    const i18n = instanceWith('en', { greet: 'Hi {who}' })
    const out = i18n.t('greet', { who: 'Nusa' })
    expect(out).toBe('Hi Nusa')
    // Stated separately, because this exact string is what the broken wiring
    // returned while every other assertion about i18n still passed.
    expect(out).not.toBe('Hi {who}')
  })

  it('selects English plural categories, including the zero case', () => {
    const i18n = instanceWith('en', {
      n: '{count, plural, =0 {no accounts} one {# account} other {# accounts}}',
    })
    expect(i18n.t('n', { count: 0 })).toBe('no accounts')
    expect(i18n.t('n', { count: 1 })).toBe('1 account')
    expect(i18n.t('n', { count: 7 })).toBe('7 accounts')
  })

  it('gives Indonesian one form for every count, because it has no plural', () => {
    const i18n = instanceWith('id', { n: '{count, plural, other {# akun}}' })
    expect(i18n.t('n', { count: 1 })).toBe('1 akun')
    expect(i18n.t('n', { count: 7 })).toBe('7 akun')
  })

  it('handles select, which is how a gendered or typed message is written', () => {
    const i18n = instanceWith('en', {
      s: '{kind, select, correction {a correction} deletion {a deletion} other {an entry}}',
    })
    expect(i18n.t('s', { kind: 'deletion' })).toBe('a deletion')
    expect(i18n.t('s', { kind: 'whatever' })).toBe('an entry')
  })
})

describe('a broken message fails loudly', () => {
  let errors: unknown[][] = []

  beforeEach(() => {
    errors = []
    vi.spyOn(console, 'error').mockImplementation((...args: unknown[]) => {
      errors.push(args)
    })
  })
  afterEach(() => vi.restoreAllMocks())

  it('reports the key rather than swallowing the failure', () => {
    const i18n = instanceWith('en', { bad: '{count, plural, one {#}' })
    // The raw string is still returned — a mistyped catalogue entry must not
    // blank a screen — but it is no longer returned in silence, which is the
    // thing that hid this bug from M0 to M3.
    expect(i18n.t('bad', { count: 1 })).toBe('{count, plural, one {#}')
    expect(errors).toHaveLength(1)
    expect(String(errors[0]?.[0])).toContain('bad')
  })
})
