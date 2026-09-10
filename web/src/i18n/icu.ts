import { IntlMessageFormat } from 'intl-messageformat'

/**
 * Cache-key separator, written as an escape rather than typed. U+001F cannot
 * occur in a language tag, a namespace or a message, so no two keys collide.
 * A byte scan found raw control characters here where spaces had been written;
 * a character no reader can see is one a copy-paste eventually eats.
 */
const SEP = '\u001F'

/**
 * ICU MessageFormat for i18next, wired directly to `intl-messageformat`.
 *
 * This replaces the `i18next-icu` package, and the reason is worth keeping
 * because it is the whole M0 lesson in one line.
 *
 * `i18next-icu@2.4.4` imports the formatter as a default export and calls
 * `new IntlMessageFormat(...)`. `intl-messageformat@10` has no default export —
 * `import x from 'intl-messageformat'` yields the module namespace object — so
 * every call threw `IntlMessageFormat is not a constructor`. Its default
 * `parseErrorHandler` catches that and returns the *unformatted* string, so
 * `t('m', { who: 'X' })` answered "Hi {who}" and nothing anywhere said why.
 *
 * ICU has therefore never once formatted a message in this project. It went
 * unnoticed from M0 to M3 for exactly one reason: no message in either
 * catalogue had a placeholder in it, so there was nothing for the failure to
 * spoil. M0 recorded "ICU MessageFormat wired from the first string" as a
 * decision it had proved; what it had proved was that language switching works.
 *
 * Two things follow, and both are deliberate:
 *
 *   - A parse failure is LOUD. It still returns the raw string rather than
 *     blanking a screen over a mistyped catalogue entry, but it reports the
 *     key and the error rather than swallowing them. Silence is what cost two
 *     milestones here.
 *   - `icu.test.ts` asserts that formatting actually happens — interpolation,
 *     English plural categories, and Indonesian, which has no plural forms at
 *     all. A wiring that reports success without formatting anything cannot
 *     pass those.
 */
class ICUFormat {
  static readonly type = 'i18nFormat'
  readonly type = 'i18nFormat' as const

  readonly #cache = new Map<string, IntlMessageFormat>()

  init(): void {
    // Nothing to configure. The formatter takes its locale per call.
  }

  parse(
    res: string,
    options: Record<string, unknown>,
    lng: string,
    ns: string,
    key: string,
  ): string {
    if (typeof res !== 'string') return res

    const cacheKey = [lng, ns, key, res].join(SEP)
    try {
      let compiled = this.#cache.get(cacheKey)
      if (compiled === undefined) {
        // ignoreTag, so that <Trans> placeholders like <0></0> reach
        // react-i18next intact instead of being demanded as format arguments.
        compiled = new IntlMessageFormat(res, lng, undefined, { ignoreTag: true })
        this.#cache.set(cacheKey, compiled)
      }
      const out = compiled.format(options)
      return Array.isArray(out) ? out.join('') : String(out)
    } catch (error) {
      console.error(`i18n: cannot format ${lng}.${ns}.${key} —`, error)
      return res
    }
  }
}

export default ICUFormat
