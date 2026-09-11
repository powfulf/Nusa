import { render, screen } from '@testing-library/react'
import { Inbox } from 'lucide-react'
import { describe, expect, it, vi } from 'vitest'

import { EmptyState } from './EmptyState'
import { Region, regionStateFrom, type RegionState } from './Region'
import { SkeletonAmount, SkeletonBlock, SkeletonRegion, SkeletonText } from './Skeleton'
import '../i18n'

/*
 * THE NEGATIVE PROOF, checked by the compiler.
 *
 * Each line below is a state DESIGN.md forbids, written as a value. If the
 * type ever grows a way to express one of them, the @ts-expect-error directive
 * becomes unused and `tsc --noEmit` fails — so the guard is the type checker
 * and the break is "make the forbidden thing typecheck".
 */
// @ts-expect-error — loading AND carrying data: a mixed region has no shape
const _mixed: RegionState<string[]> = { kind: 'loading', data: [] }
// @ts-expect-error — loading AND empty at once has no shape
const _loadingEmpty: RegionState<string[]> = { kind: 'loading', empty: true }
// @ts-expect-error — the boolean-triple shape that lets a fetch reach the empty branch
const _booleans: RegionState<string[]> = { isLoading: true, data: [] }
// @ts-expect-error — an error is not an empty state
const _errorAsEmpty: RegionState<string[]> = { kind: 'empty', error: new Error('x') }
void [_mixed, _loadingEmpty, _booleans, _errorAsEmpty]

const fixtures = {
  skeleton: () => <SkeletonText lines={3} />,
  empty: () => (
    <EmptyState
      icon={<Inbox size={32} />}
      heading="Nothing yet"
      explanation="Add the first one."
      action="Add"
      onAction={vi.fn()}
    />
  ),
  failed: (error: unknown) => <span>Failed: {String(error)}</span>,
  ready: (data: string[]) => <ul>{data.map((d) => <li key={d}>{d}</li>)}</ul>,
}

describe('<Region>', () => {
  it('renders exactly one branch for each state', () => {
    const cases: ReadonlyArray<readonly [RegionState<string[]>, string]> = [
      [{ kind: 'loading' }, 'Loading'],
      [{ kind: 'empty' }, 'Nothing yet'],
      [{ kind: 'error', error: 'boom' }, 'Failed: boom'],
      [{ kind: 'ready', data: ['a', 'b'] }, 'a'],
    ]
    for (const [state, expected] of cases) {
      const { unmount } = render(<Region state={state} {...fixtures} />)
      expect(screen.getByText(expected)).toBeTruthy()
      // The other three branches are absent — that is the "never a mixture" rule.
      // Exact matches, because a substring match finds the "a" inside "Loading"
      // and reports a mixture that is not there — the first draft did.
      const present = [
        screen.queryByText('Loading'),
        screen.queryByText('Nothing yet'),
        screen.queryByText(/^Failed: /),
        screen.queryByText('a'),
      ].filter((el) => el !== null)
      expect(present, `state ${state.kind}`).toHaveLength(1)
      unmount()
    }
  })

  it('marks a loading region busy and announces it once, not per bar', () => {
    render(<Region state={{ kind: 'loading' }} {...fixtures} />)
    expect(document.querySelector('[aria-busy="true"]')).toBeTruthy()
    expect(screen.getAllByRole('status')).toHaveLength(1)
    // Three lines drawn, none of them reachable by a screen reader.
    expect(document.querySelectorAll('.skeleton-line[aria-hidden="true"]')).toHaveLength(3)
  })

  it('never turns a pending query into an empty state', () => {
    // The bridge from TanStack Query. An empty array during a fetch is
    // "loading", and only after success is it "empty". Removing the status
    // branch and looking at data alone would make this go red.
    const isEmpty = (d: string[]) => d.length === 0
    expect(regionStateFrom({ status: 'pending', data: undefined, error: null }, isEmpty).kind).toBe('loading')
    expect(regionStateFrom({ status: 'pending', data: [], error: null }, isEmpty).kind).toBe('loading')
    expect(regionStateFrom({ status: 'success', data: [], error: null }, isEmpty).kind).toBe('empty')
    expect(regionStateFrom({ status: 'success', data: ['x'], error: null }, isEmpty).kind).toBe('ready')
    expect(regionStateFrom({ status: 'error', data: [], error: 'e' }, isEmpty).kind).toBe('error')
  })
})

describe('skeleton geometry', () => {
  it('shortens only the last of several lines, and never a single one', () => {
    const { container, unmount } = render(<SkeletonText lines={3} />)
    const widths = [...container.querySelectorAll('.skeleton-bar')].map((b) => b.className.includes('w-3/5'))
    expect(widths).toEqual([false, false, true])
    unmount()
    const single = render(<SkeletonText lines={1} />)
    expect(single.container.querySelector('.skeleton-bar')?.className).toContain('w-full')
  })

  it('keeps a numeric skeleton full width and aligned to the inline end', () => {
    const { container } = render(<SkeletonAmount />)
    const line = container.querySelector('.skeleton-line')
    expect(line?.className).toContain('numeric')
    expect(container.querySelector('.skeleton-bar')?.className).toContain('w-full')
  })

  it('takes the radius of the shape it replaces', () => {
    const { container } = render(
      <>
        <SkeletonBlock height="h-2xl" radius="full" />
        <SkeletonBlock height="h-2xl" />
      </>,
    )
    const [avatar, card] = container.querySelectorAll('span')
    expect(avatar?.className).toContain('rounded-full')
    expect(card?.className.split(' ')).toContain('rounded')
  })

  it('carries the pulse on the region, once', () => {
    const { container } = render(
      <SkeletonRegion>
        <SkeletonText lines={4} />
      </SkeletonRegion>,
    )
    expect(container.querySelectorAll('.animate-pulse')).toHaveLength(1)
  })
})

describe('<EmptyState>', () => {
  it('has exactly one action, and it is a primary button', () => {
    const onAction = vi.fn()
    render(
      <EmptyState
        icon={<Inbox size={32} />}
        heading="No transactions yet"
        explanation="A transaction is money moving. Add the first one."
        action="Add a transaction"
        onAction={onAction}
      />,
    )
    const buttons = screen.getAllByRole('button')
    expect(buttons).toHaveLength(1)
    buttons[0]?.click()
    expect(onAction).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('heading', { level: 3 }).textContent).toBe('No transactions yet')
  })
})
