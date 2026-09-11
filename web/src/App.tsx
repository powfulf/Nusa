import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { HealthCard } from './screens/HealthCard'
import { LanguageSwitcher } from './components/LanguageSwitcher'

export function App() {
  const { t } = useTranslation()

  // The tab title is a user-facing string too, so it follows the chosen
  // language rather than staying frozen at whatever the build put there.
  const appName = t('app.name')
  useEffect(() => {
    document.title = appName
  }, [appName])

  return (
    // The shell caps at --measure-shell. Past that the gutters grow and the
    // content does not: a 1920px-wide transaction row is unreadable because
    // the eye loses the line between date and amount.
    <div className="mx-auto flex max-w-shell flex-col gap-xl px-md py-xl md:px-xl md:py-2xl">
      <header className="flex flex-wrap items-start justify-between gap-md">
        <div>
          <h1 className="text-h1 font-bold text-text-primary">{t('app.name')}</h1>
          <p className="mt-xs text-text-secondary">{t('app.tagline')}</p>
        </div>
        <LanguageSwitcher />
      </header>

      <main className="flex max-w-prose flex-col gap-lg">
        <HealthCard />
        <p className="text-body-sm text-text-muted">{t('milestone.notice')}</p>
      </main>
    </div>
  )
}
