import { useTranslation } from 'react-i18next'

import { entries } from './entries'
import { GALLERY_MARKER, REQUIRED_STATES, type StateKey, type StateSpec } from './registry'

/**
 * Every primitive, in every state, twice: once as shipped and once under
 * `data-effects="reduced"`, which flattens elevation and stops motion.
 *
 * Development only. The route is registered behind `import.meta.env.DEV` and
 * loaded lazily, so this module is dead code to the production build; the
 * marker below is how `scripts/check-gallery-bundle.mjs` proves that rather
 * than trusting it. Playwright drives the `interactive` states here — hover,
 * focus, active — because a static page cannot force a pointer condition
 * honestly.
 */
export default function GalleryRoute() {
  const { t } = useTranslation()

  return (
    <main
      data-gallery={GALLERY_MARKER}
      className="mx-auto flex max-w-shell flex-col gap-2xl px-md py-xl md:px-xl"
    >
      <h1 className="text-h1 font-bold text-text-primary">{t('gallery.title')}</h1>

      {(['normal', 'reduced'] as const).map((effects) => (
        <section
          key={effects}
          data-effects={effects === 'reduced' ? 'reduced' : undefined}
          className="flex flex-col gap-xl"
        >
          <h2 className="text-h2 font-semibold text-text-primary">
            {t(effects === 'reduced' ? 'gallery.effects.reduced' : 'gallery.effects.normal')}
          </h2>

          {Object.entries(entries).map(([name, entry]) => (
            <section
              key={name}
              data-gallery-entry={name}
              className="flex flex-col gap-md rounded border border-border-subtle bg-surface p-lg"
            >
              <h3 className="text-h3 font-semibold text-text-primary">{t(entry.titleKey)}</h3>
              <div className="grid grid-cols-1 gap-md md:grid-cols-2 lg:grid-cols-4">
                {REQUIRED_STATES.map((state) => (
                  <StateCell key={state} name={name} state={state} spec={entry.states[state]} />
                ))}
              </div>
            </section>
          ))}
        </section>
      ))}
    </main>
  )
}

function StateCell({ name, state, spec }: { name: string; state: StateKey; spec: StateSpec }) {
  const { t } = useTranslation()

  return (
    <div
      data-gallery-state={state}
      data-gallery-kind={'render' in spec ? 'render' : 'interactive' in spec ? 'interactive' : 'n/a'}
      className="flex flex-col gap-sm rounded border border-border-subtle bg-surface-base p-md"
    >
      <h4 className="text-caption font-medium uppercase tracking-wide text-text-secondary">
        {t(`gallery.state.${state}`)}
      </h4>
      {'render' in spec && <div data-gallery-render={`${name}:${state}`}>{spec.render()}</div>}
      {'interactive' in spec && (
        <p className="text-body-sm text-text-muted">{t('gallery.driven_by_playwright')}</p>
      )}
      {'notApplicable' in spec && (
        <p className="text-body-sm text-text-muted">{t('gallery.not_applicable')}</p>
      )}
    </div>
  )
}
