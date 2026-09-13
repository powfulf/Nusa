import { useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'

import { Checkbox } from '../components/Choice'
import { LanguageSwitcher } from '../components/LanguageSwitcher'
import { usePreferences } from '../preferences/store'

/**
 * The two toggles DESIGN.md gives the settings screen, plus the language.
 *
 * Both toggles are checkboxes rather than switches, because a switch is a
 * component DESIGN.md does not specify and inventing one is what the M3
 * prompt forbids. A checkbox with a permanent label and a description is
 * everything the setting needs.
 *
 * Density is offered at every width even though it only takes effect at `md`
 * and above (tokens.css gates the override). The preference belongs to the
 * person, not the device — switched on from a phone, it is waiting on the
 * desktop — and the description says where it applies rather than the
 * control disappearing and leaving someone to wonder where it went.
 *
 * When the system already asks for reduced motion, the effects toggle says
 * so: motion is off either way (DESIGN.md § Motion — the two combine as a
 * union), and a toggle that appears to control something it does not would
 * be a lie the person finds out by watching nothing change.
 */
export function Settings() {
  const { t } = useTranslation()
  const effects = usePreferences((s) => s.effects)
  const density = usePreferences((s) => s.density)
  const setEffects = usePreferences((s) => s.setEffects)
  const setDensity = usePreferences((s) => s.setDensity)
  const systemReducesMotion = useSyncExternalStore(subscribeReducedMotion, readReducedMotion, () => false)

  return (
    <div className="flex max-w-prose flex-col gap-xl">
      <section className="flex flex-col gap-md">
        <Checkbox
          label={t('settings.effects.label')}
          description={
            systemReducesMotion
              ? `${t('settings.effects.description')} ${t('settings.effects.systemReduced')}`
              : t('settings.effects.description')
          }
          checked={effects === 'reduced'}
          onChange={(e) => setEffects(e.target.checked ? 'reduced' : 'normal')}
        />
        <Checkbox
          label={t('settings.density.label')}
          description={t('settings.density.description')}
          checked={density === 'dense'}
          onChange={(e) => setDensity(e.target.checked ? 'dense' : 'comfortable')}
        />
      </section>
      <section className="flex flex-col gap-sm">
        <h2 className="text-h4 font-medium text-text-primary">{t('language.label')}</h2>
        <LanguageSwitcher />
      </section>
    </div>
  )
}

const REDUCED_MOTION = '(prefers-reduced-motion: reduce)'

function readReducedMotion(): boolean {
  return typeof matchMedia === 'function' && matchMedia(REDUCED_MOTION).matches
}

function subscribeReducedMotion(onChange: () => void): () => void {
  if (typeof matchMedia !== 'function') return () => {}
  const query = matchMedia(REDUCED_MOTION)
  query.addEventListener('change', onChange)
  return () => query.removeEventListener('change', onChange)
}
