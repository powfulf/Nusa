import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2, Loader2, XCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { fetchHealth } from '../api/health'

/**
 * Reports whether the server and its database are reachable.
 *
 * Every state carries an icon and a sentence as well as a colour, because
 * colour is never the sole carrier of meaning. The icons use the CONTENT value
 * of their status colour, not the FILL value — an icon that conveys something
 * has to be readable, and the fill values do not clear 3:1.
 */
export function HealthCard() {
  const { t } = useTranslation()

  const { data, error, isPending, refetch, isFetching } = useQuery({
    queryKey: ['health'],
    queryFn: ({ signal }) => fetchHealth(signal),
    retry: false,
  })

  const state = isPending
    ? { Icon: Loader2, tone: 'text-text-secondary', message: t('health.checking') }
    : error
      ? { Icon: XCircle, tone: 'text-error-content', message: t('health.unreachable') }
      : data?.status === 'ok'
        ? { Icon: CheckCircle2, tone: 'text-success-content', message: t('health.ok') }
        : { Icon: AlertTriangle, tone: 'text-warning-content', message: t('health.unavailable') }

  const schemaVersion = data?.checks.database.schema_version

  return (
    <section className="rounded border border-border-subtle bg-surface p-lg">
      <h2 className="text-caption font-medium uppercase tracking-wide text-text-secondary">
        {t('health.title')}
      </h2>

      <p aria-live="polite" className="mt-sm flex items-center gap-sm text-body-lg">
        <state.Icon aria-hidden="true" size={20} strokeWidth={2} className={state.tone} />
        <span className="text-text-primary">{state.message}</span>
      </p>

      {schemaVersion !== undefined && (
        <dl className="mt-md border-t border-border-subtle pt-md text-body-sm">
          <div className="flex items-baseline justify-between gap-md">
            <dt className="text-text-muted">{t('health.schemaVersion')}</dt>
            <dd className="numeric text-text-primary">{schemaVersion}</dd>
          </div>
        </dl>
      )}

      {/*
        A labelled control, so its border is a supporting cue rather than the
        only thing identifying it. The fill also differs from the card behind
        it, which is the second indicator DESIGN.md requires.
      */}
      <button
        type="button"
        onClick={() => void refetch()}
        disabled={isFetching}
        className="mt-md min-h-touch rounded bg-control-primary px-md py-sm text-body-sm text-control-primary-text transition-colors duration-fast ease-standard hover:bg-control-primary-hover disabled:cursor-not-allowed disabled:opacity-40 md:min-h-0 focus-inverse"
      >
        {t('health.retry')}
      </button>
    </section>
  )
}
