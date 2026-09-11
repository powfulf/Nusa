import { useTranslation } from 'react-i18next'

import { FilterChip } from './Chip'
import { languages, rememberLanguage, type Language } from '../i18n'

/**
 * Switches the interface language.
 *
 * Each language is offered under its own name, because someone who cannot read
 * the current language still needs to find their own.
 *
 * A group of filter chips. The active one is identified by its fill and by
 * aria-pressed, not by colour alone, and each carries a permanent text label —
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
      {languages.map((language) => (
        <FilterChip
          key={language}
          lang={language}
          active={i18n.resolvedLanguage === language}
          onClick={() => change(language)}
        >
          {t(`language.${language}`)}
        </FilterChip>
      ))}
    </nav>
  )
}
