import { useTranslation } from 'react-i18next'

import { HealthCard } from './HealthCard'

/**
 * The landing screen. In M3 it carries what M0 built — the server status —
 * and the milestone notice; the dashboard proper arrives with the first
 * balances (M6).
 */
export function Overview() {
  const { t } = useTranslation()
  return (
    <div className="flex max-w-prose flex-col gap-lg">
      <HealthCard />
      <p className="text-body-sm text-text-muted">{t('milestone.notice')}</p>
    </div>
  )
}
