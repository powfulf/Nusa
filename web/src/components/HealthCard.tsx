import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { fetchHealth } from '../api/health'

/**
 * Reports whether the server and its database are reachable.
 *
 * This sits on a data surface: opaque, high contrast, no gradients. Status is
 * never carried by colour alone — every state has a symbol and a sentence.
 */
export function HealthCard() {
  const { t } = useTranslation()

  const { data, error, isPending, refetch, isFetching } = useQuery({
    queryKey: ['health'],
    queryFn: ({ signal }) => fetchHealth(signal),
    retry: false,
  })

  const state = isPending
    ? { symbol: '…', tone: 'text-fg-secondary', message: t('health.checking') }
    : error
      ? { symbol: '×', tone: 'text-danger', message: t('health.unreachable') }
      : data?.status === 'ok'
        ? { symbol: '✓', tone: 'text-accent-alt', message: t('health.ok') }
        : { symbol: '!', tone: 'text-warn', message: t('health.unavailable') }

  const schemaVersion = data?.checks.database.schema_version

  return (
    <section className="rounded-card border border-bevel bg-surface-data p-6">
      <h2 className="text-sm font-medium uppercase tracking-wide text-fg-muted">
        {t('health.title')}
      </h2>

      <p aria-live="polite" className="mt-3 flex items-center gap-3 text-lg">
        <span aria-hidden="true" className={`${state.tone} text-2xl leading-none`}>
          {state.symbol}
        </span>
        <span className="text-fg-primary">{state.message}</span>
      </p>

      {schemaVersion !== undefined && (
        <dl className="mt-5 border-t border-bevel pt-4 text-sm">
          <div className="flex items-baseline justify-between gap-4">
            <dt className="text-fg-secondary">{t('health.schemaVersion')}</dt>
            <dd className="tabular text-fg-primary">{schemaVersion}</dd>
          </div>
        </dl>
      )}

      <button
        type="button"
        onClick={() => void refetch()}
        disabled={isFetching}
        className="mt-5 rounded-button bg-surface-raised px-4 py-2 text-sm text-fg-primary ring-1 ring-bevel transition-opacity hover:opacity-90 disabled:opacity-50"
      >
        {t('health.retry')}
      </button>
    </section>
  )
}
