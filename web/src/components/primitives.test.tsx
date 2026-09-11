import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { FilterChip, StatusChip } from './Chip'
import { Input } from './Input'
import { List, ListRow } from './List'
import { Table, TableRow, type TableColumn } from './Table'
import '../i18n'

describe('<FilterChip>', () => {
  it('is a button whose selection is exposed as aria-pressed, not colour alone', () => {
    render(
      <>
        <FilterChip active>All</FilterChip>
        <FilterChip>Some</FilterChip>
      </>,
    )
    expect(screen.getByRole('button', { name: 'All', pressed: true })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Some', pressed: false })).toBeTruthy()
  })

  it('leaves the underlying text untouched by the uppercase styling', () => {
    // Uppercase is a CSS transform; what a screen reader gets and what is
    // copied stays what the catalogue said.
    render(<FilterChip>Semua</FilterChip>)
    expect(screen.getByRole('button').textContent).toBe('Semua')
  })
})

describe('<StatusChip>', () => {
  it('is not a control', () => {
    render(<StatusChip tone="warning">Waiting</StatusChip>)
    expect(screen.queryByRole('button')).toBeNull()
    expect(screen.getByText('Waiting').tagName).toBe('SPAN')
  })

  it('hides a decorative icon from assistive technology', () => {
    const { container } = render(
      <StatusChip tone="success" icon={<svg data-testid="icon" />}>
        Matched
      </StatusChip>,
    )
    expect(container.querySelector('[aria-hidden="true"] [data-testid="icon"]')).toBeTruthy()
  })
})

describe('<Input>', () => {
  it('always renders a visible label associated with the field', () => {
    render(<Input label="Account name" />)
    const field = screen.getByLabelText('Account name')
    expect(field.tagName).toBe('INPUT')
    expect(screen.getByText('Account name').tagName).toBe('LABEL')
  })

  it('has no placeholder prop, because a placeholder is not a label', () => {
    render(<Input label="Account name" />)
    expect(screen.getByLabelText('Account name').getAttribute('placeholder')).toBeNull()
  })

  it('associates the error and announces it, replacing the helper', () => {
    render(<Input label="Name" helper="Helper text" error="It needs a name" />)
    const field = screen.getByLabelText('Name')
    expect(field.getAttribute('aria-invalid')).toBe('true')
    const described = document.getElementById(field.getAttribute('aria-describedby') ?? '')
    expect(described?.textContent).toBe('It needs a name')
    expect(described?.getAttribute('role')).toBe('alert')
    expect(screen.queryByText('Helper text')).toBeNull()
  })

  it('associates the helper when there is no error', () => {
    render(<Input label="Name" helper="Helper text" />)
    const field = screen.getByLabelText('Name')
    expect(field.getAttribute('aria-invalid')).toBe('false')
    expect(document.getElementById(field.getAttribute('aria-describedby') ?? '')?.textContent).toBe(
      'Helper text',
    )
  })

  it('carries the field class that holds the focus and error treatment', () => {
    // The visual states live in index.css under .field; this proves the
    // component actually opts in, which a class-name typo would silently not.
    render(<Input label="Name" />)
    expect(screen.getByLabelText('Name').className.split(' ')).toContain('field')
  })
})

describe('<List>', () => {
  function threeRows() {
    const onSelect = vi.fn()
    render(
      <List label="Transactions">
        <ListRow first label="One" onSelect={onSelect} />
        <ListRow label="Two" onSelect={onSelect} />
        <ListRow label="Three" onSelect={onSelect} />
      </List>,
    )
    return { rows: screen.getAllByRole('option'), onSelect }
  }

  it('is a single tab stop: exactly one row is tabbable', () => {
    const { rows } = threeRows()
    expect(rows.map((r) => r.tabIndex)).toEqual([0, -1, -1])
  })

  it('moves focus with the arrow keys and moves the tab stop with it', () => {
    const { rows } = threeRows()
    rows[0]?.focus()
    fireEvent.keyDown(screen.getByRole('listbox'), { key: 'ArrowDown' })
    expect(document.activeElement).toBe(rows[1])
    expect(rows.map((r) => r.tabIndex)).toEqual([-1, 0, -1])

    fireEvent.keyDown(screen.getByRole('listbox'), { key: 'End' })
    expect(document.activeElement).toBe(rows[2])
    fireEvent.keyDown(screen.getByRole('listbox'), { key: 'ArrowDown' })
    // Clamped at the end rather than wrapping: a wrap is a surprise, and the
    // list's edges are the one place a keyboard user can rely on.
    expect(document.activeElement).toBe(rows[2])
    fireEvent.keyDown(screen.getByRole('listbox'), { key: 'Home' })
    expect(document.activeElement).toBe(rows[0])
  })

  it('selects with Enter and Space as well as click', () => {
    const { rows, onSelect } = threeRows()
    fireEvent.keyDown(rows[0]!, { key: 'Enter' })
    fireEvent.keyDown(rows[0]!, { key: ' ' })
    fireEvent.click(rows[0]!)
    expect(onSelect).toHaveBeenCalledTimes(3)
  })

  it('marks a pending row with a word, never colour alone', () => {
    render(
      <List label="Transactions">
        <ListRow first label="One" pending />
      </List>,
    )
    expect(screen.getByText('Waiting to sync')).toBeTruthy()
  })

  it('exposes selection through aria-selected', () => {
    render(
      <List label="Transactions">
        <ListRow first label="One" selected />
        <ListRow label="Two" />
      </List>,
    )
    expect(screen.getByRole('option', { name: 'One', selected: true })).toBeTruthy()
  })
})

describe('<Table>', () => {
  const columns: readonly TableColumn[] = [
    { key: 'payee', heading: 'Payee' },
    { key: 'amount', heading: 'Amount', numeric: true },
  ]

  it('stays a table for assistive technology, with a caption and column headers', () => {
    render(
      <Table caption="Transactions" columns={columns}>
        <TableRow columns={columns} cells={{ payee: 'Shop', amount: '1,00' }} />
      </Table>,
    )
    expect(screen.getByRole('table', { name: 'Transactions' })).toBeTruthy()
    expect(screen.getAllByRole('columnheader').map((h) => h.textContent)).toEqual(['Payee', 'Amount'])
  })

  it('carries each column heading on its cells, which is what the reflow reads', () => {
    render(
      <Table caption="Transactions" columns={columns}>
        <TableRow columns={columns} cells={{ payee: 'Shop', amount: '1,00' }} />
      </Table>,
    )
    const cells = screen.getAllByRole('cell')
    expect(cells.map((c) => c.getAttribute('data-label'))).toEqual(['Payee', 'Amount'])
  })

  it('aligns a numeric column to the inline end with tabular figures', () => {
    render(
      <Table caption="Transactions" columns={columns}>
        <TableRow columns={columns} cells={{ payee: 'Shop', amount: '1,00' }} />
      </Table>,
    )
    const [payee, amount] = screen.getAllByRole('cell')
    expect(amount?.className.split(' ')).toContain('numeric')
    expect(payee?.className.split(' ')).not.toContain('numeric')
  })

  it('makes a selectable row focusable and operable by keyboard', () => {
    const onSelect = vi.fn()
    render(
      <Table caption="Transactions" columns={columns}>
        <TableRow columns={columns} cells={{ payee: 'Shop', amount: '1,00' }} onSelect={onSelect} />
      </Table>,
    )
    const row = screen.getAllByRole('row')[1]!
    expect(row.tabIndex).toBe(0)
    fireEvent.keyDown(row, { key: 'Enter' })
    expect(onSelect).toHaveBeenCalledTimes(1)
  })
})
