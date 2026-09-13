import { Hourglass } from 'lucide-react'
import { useTranslation } from 'react-i18next'

/**
 * DESIGN.md § Empty states, "Never used for a screen that does not exist yet".
 *
 * A destination the product has not built shows this and NOT an <EmptyState>.
 * An empty state makes two claims — the reader's data is empty, and the one
 * action will do something — and an unbuilt screen can make neither: the
 * shell has fetched nothing, so "you have no transactions" would be a guess,
 * and a button that leads nowhere teaches that the big button sometimes does
 * nothing. Same icon, heading and explanation geometry; no action; a
 * statement about the product rather than about anybody's money.
 */
export interface NotYetProps {
  /** Catalogue key of the screen's name, the same one the navigation uses. */
  readonly screenKey: string
}

export function NotYet({ screenKey }: NotYetProps) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-center px-md py-2xl text-center">
      <span aria-hidden="true" className="text-text-secondary">
        <Hourglass size={32} strokeWidth={2} />
      </span>
      <h2 className="mt-md max-w-narrow text-h3 font-semibold text-text-primary">
        {t('notYet.heading', { screen: t(screenKey) })}
      </h2>
      <p className="mt-sm max-w-narrow text-body-sm text-text-muted">{t('notYet.explanation')}</p>
    </div>
  )
}
