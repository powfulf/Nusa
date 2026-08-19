import { useTranslation } from 'react-i18next'

import { languages, rememberLanguage, type Language } from '../i18n'

/**
 * Switches the interface language.
 *
 * Each language is offered under its own name, because someone who cannot read
 * the current language still needs to find their own.
 *
 * These are filter chips. The active one is identified by its fill and by
 * aria-current, not by colour alone, and each carries a permanent text label —
 * so no chip depends on its border to be recognised as a control.
 */
export function LanguageSwitcher() {
  const { t, i18n } = useTranslation()

  const change = (language: Language) => {
    void i18n.changeLanguage(language)
    rememberLanguage(language)
    document.documentElement.lang = language
  }

  return (
    <nav aria-label={t('language.label')} className="flex items-center gap-sm">
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
              'min-h-touch rounded-sm px-md py-xs text-caption font-medium uppercase tracking-wide transition-colors duration-fast ease-standard md:min-h-0',
              active
                ? 'bg-control-primary text-control-primary-text focus-inverse'
                : 'border border-border-strong bg-surface-base text-text-primary hover:bg-surface-sunken',
            ].join(' ')}
          >
            {t(`language.${language}`)}
          </button>
        )
      })}
    </nav>
  )
}
