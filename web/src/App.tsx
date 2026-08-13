import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { HealthCard } from './components/HealthCard'
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
    <div className="relative min-h-dvh">
      {/*
        The ambient layer sits behind everything and never receives pointer
        events. The animated, organic version arrives in M3.
      */}
      <div aria-hidden="true" className="pointer-events-none fixed inset-0 -z-10 bg-ambient" />

      <div className="mx-auto flex max-w-2xl flex-col gap-8 px-6 py-16">
        <header className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h1 className="text-3xl font-semibold text-fg-primary">{t('app.name')}</h1>
            <p className="mt-1 text-fg-secondary">{t('app.tagline')}</p>
          </div>
          <LanguageSwitcher />
        </header>

        <main className="flex flex-col gap-6">
          <HealthCard />
          <p className="text-sm text-fg-muted">{t('milestone.notice')}</p>
        </main>
      </div>
    </div>
  )
}
