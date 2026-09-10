import i18n from 'i18next'
import ICU from './icu'
import { initReactI18next } from 'react-i18next'

import en from './en.json'
import id from './id.json'

/**
 * Supported languages, in the order they are offered.
 *
 * English is the default UI language; Indonesian is the first translation.
 * Both exist from the first milestone that renders text.
 */
export const languages = ['en', 'id'] as const
export type Language = (typeof languages)[number]

export const defaultLanguage: Language = 'en'

const storageKey = 'nusa.language'

export function isLanguage(value: string | null): value is Language {
  return value !== null && (languages as readonly string[]).includes(value)
}

/**
 * Reads the preferred language from storage, then the browser, then falls back
 * to the default.
 *
 * Language and locale are separate concerns: this picks the language of the
 * interface. Number, date and currency formatting follow the user's locale and
 * are resolved independently.
 */
function detectLanguage(): Language {
  try {
    const stored = globalThis.localStorage?.getItem(storageKey) ?? null
    if (isLanguage(stored)) return stored
  } catch {
    // Storage can be unavailable in private browsing. Detection is a nicety,
    // never a reason to fail startup.
  }

  const browser = globalThis.navigator?.language?.split('-')[0] ?? null
  return isLanguage(browser) ? browser : defaultLanguage
}

export function rememberLanguage(language: Language): void {
  try {
    globalThis.localStorage?.setItem(storageKey, language)
  } catch {
    // Not being able to remember the choice is survivable.
  }
}

/**
 * ICU MessageFormat handles plural and select rules. Indonesian has no plural
 * forms and Arabic has six; the library knows the rules, so we never hand-roll
 * them and never concatenate translated fragments.
 *
 * The adapter is ours rather than `i18next-icu`, which could not construct the
 * formatter at all and hid that behind a silent fallback. See ./icu.ts, and
 * ./icu.test.ts for the check that would have caught it.
 */
void i18n
  .use(ICU)
  .use(initReactI18next)
  .init({
    resources: {
      en: { translation: en },
      id: { translation: id },
    },
    lng: detectLanguage(),
    fallbackLng: defaultLanguage,
    interpolation: {
      // React escapes interpolated values already.
      escapeValue: false,
    },
  })

export default i18n
