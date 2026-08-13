import { useTranslation } from 'react-i18next'

import { languages, rememberLanguage, type Language } from '../i18n'

/**
 * Switches the interface language.
 *
 * Each language is offered under its own name, because someone who cannot read
 * the current language still needs to find their own.
 */
export function LanguageSwitcher() {
  const { t, i18n } = useTranslation()

  const change = (language: Language) => {
    void i18n.changeLanguage(language)
    rememberLanguage(language)
    document.documentElement.lang = language
  }

  return (
    <nav aria-label={t('language.label')} className="flex items-center gap-2">
      {languages.map((language) => {
        const active = i18n.resolvedLanguage === language
        return (
          <button
            key={language}
            type="button"
            lang={language}
            aria-current={active ? 'true' : undefined}
            onClick={() => change(language)}
            className={[
              'rounded-button px-3 py-1.5 text-sm transition-colors',
              active
                ? 'bg-surface-raised text-fg-primary ring-1 ring-bevel'
                : 'text-fg-secondary hover:text-fg-primary',
            ].join(' ')}
          >
            {t(`language.${language}`)}
          </button>
        )
      })}
    </nav>
  )
}
