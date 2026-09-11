# Nusa Design System

> This document is the single authority for how Nusa looks. Every colour,
> typeface, spacing step, radius, shadow, motion curve, icon and component
> specification originates here.
>
> Visual decisions are never recorded anywhere else — not in code comments, not
> in `tokens.css`, not in a component library. `web/src/styles/tokens.css` is
> derived from this file and holds no decisions of its own. If something needs
> changing or is missing, it changes here first.
>
> `CLAUDE.md` §8.2 holds the functional floor — contrast, numeric alignment,
> reduced motion, RTL. That floor binds any design system, including this one.
> Where this document and that floor disagree, the floor wins and this document
> is corrected.

---

## Overview

Nusa is a calm, trustworthy design system for a self-hosted personal finance
application: double-entry accounting underneath, envelope budgeting on top,
multi-currency and multi-asset throughout. Its foundation of deep navy and soft
sage greens evokes clinical precision tempered by warmth. The system
prioritizes readability, accessibility, and a sense of reassurance across every
touchpoint.

Money is the subject matter, and money is unforgiving. A misread digit is not a
cosmetic problem. The visual language is quiet on purpose: it gets out of the
way of dense numeric content and never competes with it for attention.

---

## Token naming and theming

Tokens are named for **what they do**, never for what they look like.

- Correct: `--surface-base`, `--surface-raised`, `--text-primary`,
  `--border-subtle`, `--status-error-content`
- Wrong: `--white`, `--slate-50`, `--navy`, `--light-border`

Nusa ships light-only today. A dark theme will be added later as a derived
block that redefines the same token names and nothing else. Semantic naming is
the entire precondition for that: a token called `--white` cannot be redefined
in a dark theme without lying, and a codebase full of `--white` would have to
be rewritten rather than re-themed.

No component ever references a raw colour. Components reference tokens.

### The two places a raw value is unavoidable

Both are technical limits rather than exceptions of taste, and both are named
here so that they stay narrow. An exception that is merely *unwatched* is how a
rule quietly stops applying.

| Where | What may be raw | Why nothing else will do |
| --- | --- | --- |
| `web/tailwind.config.js`, `screens` block only | the six breakpoint widths | a media query cannot read a custom property |
| `web/public/manifest.webmanifest` | `theme_color`, `background_color` | a JSON document cannot read a custom property, and the manifest is required to carry both |

**A breakpoint lives in the `screens` block and nowhere else, including
stylesheets.** A media query in `tokens.css` or `index.css` reads it back as
`theme('screens.md')`, which the build resolves; the "below `md`" band is
written `not all and (min-width: theme('screens.md'))` rather than as a
`max-width` one pixel short. Until M3 both stylesheets carried `768px` and
`767px` of their own, so a change to the breakpoint would have moved the
utilities and left the tokens behind. `lint:tokens` now refuses a number
inside any `@media` in a stylesheet.

### One thing that looks like an exception and is not

The functional floor in `CLAUDE.md` §8.2 requires logical properties
throughout, and `lint:floor` refuses the physical spacing utilities `ml-`,
`mr-`, `pl-`, `pr-` and their CSS equivalents. It permits `px-`, `py-`, `mx-`
and `my-`, which Tailwind compiles to `padding-left` **and** `padding-right`
together.

That is deliberate, and the reason is the whole test: the floor exists so
that RTL is a stylesheet change rather than a rewrite. A physical property is
a hazard exactly when it names *one* side, because under RTL that side is the
wrong one. A symmetric pair names both sides with the same value, and
mirroring it produces itself. It carries no direction, so it carries no
hazard, and refusing it would replace every `px-md` with `ps-md pe-md` for no
change in any rendered layout. The guard bans what would break, not what
resembles what would break.

The manifest's two values are **checked against the tokens they stand for** by
`npm run lint:tokens`. A manifest whose colour has drifted from the application
is a splash screen that flashes a different colour on open, and nothing about
the file itself would ever say so.

---

## Colors

### Neutrals and surfaces

| Token | Value | Use |
| --- | --- | --- |
| `--surface-base` | `#F8FAFC` | Page background |
| `--surface-default` | `#FFFFFF` | Cards, inputs, panels |
| `--surface-sunken` | `#F1F5F9` | Ghost hover, disabled fills |
| `--surface-inverse` | `#0F172A` | Tooltips, tinted card headers |
| `--text-primary` | `#0F172A` | Headings, body, numeric values |
| `--text-secondary` | `#5A697F` | Supporting text, captions |
| `--text-muted` | `#475569` | Helper text, list descriptions |
| `--text-on-inverse` | `#F8FAFC` | Text on `--surface-inverse` |
| `--border-subtle` | `#E2E8F0` | Decorative only — see below |
| `--border-strong` | `#CBD5E1` | Control boundaries with a supporting cue |
| `--divider` | `#F1F5F9` | List row separators |

`--text-secondary` was `#64748B`, which failed AA on five of the nine surfaces
it can legitimately appear on — the sunken surface and four of the five status
chip surfaces. It was darkened rather than fenced off with a rule, because a
constraint that depends on a contributor remembering it produces no error when
broken: only text that is slightly harder to read, which nobody notices.
`#5A697F` clears every surface, worst case 5.03:1.

### Borders and control identification

WCAG 1.4.11 asks for 3:1 only where a boundary is the **sole** thing
identifying a control. Neither border value reaches that, and darkening them to
roughly `#78889B` would change the soft character of the whole system. So the
borders stay soft and the requirement is met the other way: by making sure no
control ever depends on its border alone.

- `--border-subtle` (`#E2E8F0`) is **decorative only**. Dividers, card edges,
  table rules. It is never the only thing marking a control.
- `--border-strong` (`#CBD5E1`) marks control boundaries **that carry a
  supporting cue**.

**The binding rule: every interactive control must carry at least one indicator
besides its border** — a fill that differs from its surroundings, a permanently
visible label, or an icon. That makes the border a supporting cue rather than
the sole signal, which satisfies 1.4.11 without repainting the system.

The focus ring is the exception and stays at 3:1 unconditionally. Focus is the
only marker of keyboard position; there is no second cue that can rescue it.

### Status and brand — two values per colour

Every status colour has **two** values, and choosing between them is not a
matter of taste.

**Why two.** A colour bright enough to read pleasantly as a large filled shape
is almost never dark enough to carry 12px text. `#EAB308` is a good warning
fill and a catastrophic warning label: as text on white it lands at 1.92:1,
against a floor of 4.5:1. Keeping one value per colour forces a choice between
a washed-out interface and unreadable labels. Two values keeps the palette's
character in the fills and puts a verified, darker sibling wherever meaning has
to be *read*.

**The rule.**

- **FILL** — backgrounds behind other content, and purely decorative shapes.
  Never used for anything a reader must resolve.
- **CONTENT** — mandatory whenever the colour carries text, an icon, a status
  dot, a chart key, or any other mark that conveys meaning. If removing it
  would lose information, it is content.

| Role | FILL | CONTENT | Content contrast (worst sanctioned surface) |
| --- | --- | --- | --- |
| Brand / Sage | `#059669` | `#047857` | 4.96:1 |
| Success | `#22C55E` | `#15803D` | 4.58:1 |
| Warning | `#EAB308` | `#854D0E` | 6.25:1 |
| Error | `#EF4444` | `#B91C1C` | 5.83:1 |
| Info | `#0EA5E9` | `#0369A1` | 5.42:1 |

Semantics: Brand for links, primary CTAs and focus emphasis. Success for
in-budget, on-target, reconciled. Warning for approaching a limit, or a
transaction waiting to sync. Error for over budget, failed validation, an
account that will not reconcile. Info for neutral notices and new features.

### Status chip fills — opaque, never translucent

| Token | Value |
| --- | --- |
| `--status-brand-surface` | `#EAF6F3` |
| `--status-success-surface` | `#EDFAF2` |
| `--status-warning-surface` | `#FDF9EB` |
| `--status-error-surface` | `#FEF0F0` |
| `--status-info-surface` | `#EBF8FD` |

These are the exact colours the previous translucent fills produced over white,
made opaque. Translucent fills are forbidden here because a chip does not stay
over white: it sits on hovered list rows, inside tinted card headers, and on
the page background. Text over a translucent fill has a contrast ratio that
changes with whatever happens to be behind it, which means it cannot be
verified once and trusted.

---

## Typography

- **Headline Font**: Plus Jakarta Sans
- **Body Font**: DM Sans
- **Mono Font**: Fira Code

All three are self-hosted, in woff2, with `font-display: swap`. They are never
loaded from a third-party CDN: a font request carries the reader's IP address
to whoever serves it, and Nusa does not leak its users to anyone (see
`CLAUDE.md` §10).

**Only the weights below ship.** Plus Jakarta Sans 500, 600 and 700; DM Sans
400 and 500; Fira Code 400. A weight that no style in this table calls for is
not shipped.

**Subsets are `latin` and `latin-ext` only.** English and Indonesian are both
covered entirely by `latin`; `latin-ext` carries the accented characters other
European locales need. **Any non-Latin language Nusa adds — Arabic, Thai,
Chinese, Japanese, Korean, Cyrillic, Devanagari — needs its own subset added
before its translation ships**, or its text will silently fall back to a system
face and lose every typographic decision on this page. Adding a locale is
therefore a typography task as well as a translation task.

Only the faces that paint above the fold are preloaded: Plus Jakarta Sans 700
and DM Sans 400, `latin` only. Preloading more competes for bandwidth with the
thing preloading is meant to accelerate.

| Style | Font | Desktop | Mobile (< 768px) | Line height |
| --- | --- | --- | --- | --- |
| Display | Plus Jakarta Sans bold | 40px | 32px | 1.15 |
| H1 | Plus Jakarta Sans bold | 32px | 28px | 1.2 |
| H2 | Plus Jakarta Sans semibold | 24px | 22px | 1.25 |
| H3 | Plus Jakarta Sans semibold | 20px | 18px | 1.3 |
| H4 | Plus Jakarta Sans medium | 16px | 16px | 1.35 |
| Body LG | DM Sans regular | 18px | 18px | 1.6 |
| Body | DM Sans regular | 16px | 16px | 1.6 |
| Body SM | DM Sans regular | 14px | 14px | 1.5 |
| Caption | DM Sans medium | 12px | 12px | 1.4 |
| Code | Fira Code regular | 14px | 14px | 1.6 |

Body sizes never shrink on small screens. Only headings scale down, because a
40px display heading on a 360px screen consumes a third of the viewport while a
16px body line is already at the floor of comfortable reading.

### Numeric typography

Numbers are the product. They get their own rules, and these are not optional.

- **`font-variant-numeric: tabular-nums` on every numeral in the interface.**
  This is a font *feature*, not a font swap — DM Sans with tabular figures
  aligns in a column exactly as a monospace face would, while staying the body
  typeface. Fira Code is for code and identifiers, not for money.
- **Numeric columns are right-aligned.** Always. A left-aligned money column
  cannot be scanned.
- **Decimal count is consistent within a column.** Rp 15.000,00 and Rp 7,50 in
  the same column both show two decimals; a commodity with eight decimal places
  shows eight for every row in that column.
- **Negative values always carry a minus sign.** Colour is a companion, never
  the carrier. A red figure with no sign is invisible to a reader who cannot
  distinguish it, and ambiguous when printed.
- **Numbers are never truncated and never ellipsised.** If a value does not
  fit, the column widens, the layout reflows, or the label wraps — the digits
  do not. A truncated balance is a wrong balance.
- **The grouping separator and the decimal separator** follow the reader's
  locale rather than the commodity's country. **The unit** — a currency symbol
  or a commodity code — is governed by § Units below, and the two are not the
  same rule.

### Units: a currency symbol and a commodity code are different things

Both answer "what is this a quantity of", and they are governed differently
because they are different kinds of thing. **The commodity's `kind` decides
which rule applies — never whether a symbol happens to be available.**

**A currency symbol is part of the reader's typography.** Where it sits, and
whether a space separates it from the digits, is a fact about the reader's
locale rather than about the currency: `$1,250.50` in en-US, `Rp 1.250,50` in
id-ID, `1.250,50 €` in de-DE. The locale decides all of it, so Nusa takes the
placement and the spacing from the locale's own conventions rather than
imposing one. Where the locale uses a space it is a non-breaking one — which is
what the platform already produces — so a symbol never separates from its
amount across a line break.

A currency the reader's locale has no symbol for shows its ISO code **in the
symbol's position**, because that is what the locale does with a currency it
does not abbreviate. `IDR 1,250.50` in en-US, `1.250,50 IDR` in de-DE.

**A commodity code is a unit of measure, and follows the number.** Equities,
funds, metals and crypto are counted, not priced in — `12,5000 XAU_GRAM`,
`0,15 BTC`, `1.250 BBCA.JK` — and every convention a reader already knows for
those puts the unit after the quantity, in every locale. The separator is a
non-breaking space, always, because no locale has an opinion here to follow.

| Kind | Unit | Position | Separator |
| --- | --- | --- | --- |
| `currency` | the locale's symbol, or the ISO code | wherever the locale puts it | whatever the locale uses, non-breaking |
| everything else | the commodity code | after the amount | non-breaking space, always |

**The minus sign stays with the digits**, on both sides of this rule, even
where a locale would place it outside the symbol. A column in which the sign
sometimes precedes a symbol and sometimes the digits does not align, and
alignment is most of what a numeric column is for.

Where a column heading already names the unit, the unit is omitted from every
cell and the digits stand alone. Repeating it on a thousand rows is noise the
heading has already removed.

This section replaced a single line that said symbols sit before the amount
with a space, with `$ 1,250.00` as its example. That line was written against
Rupiah and Dollars and generalised: en-US puts no space after `$`, and de-DE
and fr-FR put the symbol after the amount. It was wrong in three ways and
looked right in the two locales anyone here checked, which is the same shape as
`--text-secondary` failing on five surfaces after being examined on one.

---

## Spacing

Base unit: **8px**

| Step | Value | Use |
| --- | --- | --- |
| xs | 4px | Inline icon gaps |
| sm | 8px | Tight component padding |
| md | 16px | Default padding |
| lg | 24px | Card padding |
| xl | 32px | Section gaps |
| 2xl | 48px | Layout sections |
| 3xl | 64px | Page-level spacing |

---

## Border Radius

| Step | Value | Use |
| --- | --- | --- |
| sm | 4px | Badges, small tags |
| DEFAULT | 8px | Buttons, cards, inputs |
| md | 12px | Modals, dropdown panels |
| lg | 16px | Large containers, hero sections |
| full | 9999px | Avatars, status indicators |

---

## Elevation

Gentle, diffused shadows — precise yet approachable.

| Step | Value | Use |
| --- | --- | --- |
| sm | 1px offset, 3px blur, `#0F172A` at 3% | Buttons, chips |
| DEFAULT | 2px offset, 6px blur, `#0F172A` at 5% | Cards, dropdowns |
| md | 4px offset, 16px blur, `#0F172A` at 7% | Elevated cards |
| lg | 8px offset, 32px blur, `#0F172A` at 10% | Modals, panels |

---

## Motion

| Token | Duration | Use |
| --- | --- | --- |
| `--motion-instant` | 100ms | Hover and active colour changes |
| `--motion-fast` | 150ms | Tooltips, chips, small state changes |
| `--motion-base` | 200ms | Dropdowns, accordions, tab switches |
| `--motion-slow` | 300ms | Modals, drawers, page transitions |

| Token | Curve | Use |
| --- | --- | --- |
| `--ease-standard` | `cubic-bezier(0.2, 0, 0, 1)` | Most transitions |
| `--ease-enter` | `cubic-bezier(0, 0, 0, 1)` | Elements arriving |
| `--ease-exit` | `cubic-bezier(0.3, 0, 1, 1)` | Elements leaving |

Tooltips show after 150ms and hide immediately.

**Under `prefers-reduced-motion: reduce`,** every duration collapses to
effectively zero and no element moves, scales, or slides. State changes still
happen — they just happen at once. Nothing conveys information by movement
alone, so nothing is lost. This is honoured globally, so no component has to
remember it.

---

## Responsiveness

Mobile-first. The tested range is **360px to 1920px**.

**360px is the tested floor, not an edge case.** It is deliberately narrower
than the smallest breakpoint: entry-level Android devices are the primary phone
for much of Nusa's audience, and the `base` band has to be laid out and
verified at 360px rather than at 480px. Any layout that only works from 480px
up is broken for a large share of real users.

| Breakpoint | Min width | Shape |
| --- | --- | --- |
| base | 0 (verified at 360px) | Single column, bottom navigation |
| sm | 480px | Single column, roomier padding |
| md | 768px | Sidebar navigation appears, tables regain columns |
| lg | 1024px | Two-column layouts, persistent sidebar |
| xl | 1280px | Maximum content width reached |
| 2xl | 1536px | Additional gutter only; content does not grow |

**Navigation.** Below `md` the primary navigation is a bottom bar. At `md` and
above it becomes a persistent left sidebar. The switch happens at one
breakpoint with no intermediate state — a navigation that is half-sidebar,
half-bar is a navigation nobody can learn. The transition is a plain swap; the
bar does not animate into the sidebar.

**Touch targets are at least 44×44px below `md`, without exception.** Where a
control is visually smaller — a 18px checkbox, a small chip — its hit area is
padded out to 44px. Visual size and hit size are separate concerns.

**One-handed reach.** On mobile, primary actions live in the bottom third of
the viewport. Quick-add for a transaction has to be comfortable with one thumb,
standing in a queue at a till. Destructive and rarely-used actions go to the
top, where they are harder to hit by accident.

**Safe areas.** Layout respects `env(safe-area-inset-*)` on all four sides.
Bottom navigation and fixed action buttons add the bottom inset to their
padding so they clear the home indicator; full-bleed headers add the top inset
so they clear a notch or a camera cutout.

**Content width.** Prose and form columns cap at **72 characters**. The
application shell caps at **1280px**. Beyond that the gutters grow and the
content does not: a 1920px-wide transaction row is unreadable because the eye
loses the line between date and amount.

---

## Dense data patterns

A transaction register is dense, and that is the shape of the product rather
than a design failure. Density is not the problem; **unstructured** density is.
The patterns below are what make a long, information-rich list calm.

**Rows.**

| Property | Below `md` | `md` and above | Dense mode (`md`+) |
| --- | --- | --- | --- |
| Row height | 56px | 48px | 40px |
| Row padding | 12px 16px | 8px 16px | 4px 16px |
| Divider | 1px `--divider` | 1px `--divider` | 1px `--divider` |

**Dense mode** is an opt-in preference for large screens, for someone combing
through thousands of transactions where 8 pixels a row is several rows of
additional context per screen. It changes row height and padding only — never
type size, never the numeric rules. It is unavailable below `md`, where the
44px touch minimum governs instead.

Separators, not zebra striping. Pick one; using both produces a grid that
fights the numbers for attention. Nusa uses separators because they survive
row reordering, filtering and virtual scrolling without the stripes shuffling.

**Sticky headers.** Table headers stick to the top of the scroll container and
gain the `sm` elevation once content scrolls beneath them, so a column heading
is never lost. On mobile, the date group heading sticks instead of the column
row.

**Reflow below `md`.** A table becomes a list of cards. The date and the amount
survive as the primary line — those are what a person scans for. Description,
account and category move to a second line at Body SM in `--text-muted`. No
column is hidden outright without a way to reach it; anything dropped from the
card is available on the row's detail view.

**Amounts keep their alignment inside the card.** Tabular figures, aligned to
the inline end, consistent decimals — exactly as in the table. Losing numeric
alignment is the single biggest loss when a table reflows: a column of amounts
that no longer lines up cannot be scanned or compared, which is most of what
the list was for.

**Row states.**

| State | Treatment |
| --- | --- |
| Hover | `--surface-base` background |
| Selected | `#0F172A06` background, 2px `--brand-content` inline-start bar |
| Focus (keyboard) | Standard focus ring, inset so it is not clipped by the row |
| Pending sync | Warning chip in the row, plus a text label — never colour alone |

Rows are focusable and operable by keyboard in a single tab stop per row, with
arrow keys moving between rows.

**Numeric columns** follow the numeric typography rules without exception:
tabular figures, right-aligned, consistent decimals, never truncated.

---

## Component states

Every component specifies all of: default, hover, focus-visible, active,
disabled, loading, and error where applicable.

**Focus.** The focus ring is 2px `--text-primary` with a 2px offset, and it
must be plainly visible on *every* surface. On dark fills — primary buttons,
active filter chips, tinted card headers — the ring switches to
`--focus-ring-inverse` (`#FFFFFF`), which clears 17.85:1 there. A ring that
disappears on the primary button makes keyboard navigation unusable exactly
where it is needed most. Full keyboard operation on desktop is a requirement,
not an enhancement.

**Disabled** is 0.4 opacity, `cursor: not-allowed`, and all hover and focus
treatments suppressed.

**Loading** uses skeletons sized to the content they replace — same line
height, same number of lines, same column widths. The layout does not shift
when data arrives. A skeleton that is the wrong size is worse than a spinner,
because it promises a shape it will not deliver. The geometry that makes that
achievable rather than aspirational is specified under § Skeletons.

**Reduced effects.** When `data-effects="reduced"` is set, shadows flatten to a
1px `--border-subtle` outline and all transitions collapse. Colour, spacing and
type are untouched — the interface stays complete, it just stops moving and
floating.

---

## Icons

**Family: Lucide**, on a 24px grid, ISC licensed. Chosen because Country Pack
specifications already name Lucide icons for spending categories (`utensils`,
`fuel`, `home`), so the pack format and the interface draw from one vocabulary
instead of maintaining a mapping between two.

| Size | Use |
| --- | --- |
| 16px | Inline with Body SM and Caption, chip leading icons |
| 20px | Inline with Body, list row icons, input adornments |
| 24px | Buttons, navigation, section headers |
| 32px | Empty states, feature callouts |

**Stroke width** is 2px by default, and **1.5px below 20px** — a 2px stroke on
a 16px glyph fills in its own counters and reads as a smudge.

- An icon that **stands alone as a control** carries an `aria-label`.
- An icon that **decorates a labelled control** carries `aria-hidden`.
- **Icon colour follows the two-role rule.** An icon carrying meaning uses the
  CONTENT value; only a purely decorative shape may use a FILL value.
- **Directional icons are mirrored under RTL** — arrows, chevrons, back and
  forward, indent and outdent, progress. Icons depicting objects are not:
  a clock, a house and a fuel pump point the same way in every script.
- **Touch targets still govern.** A 16px or 20px icon button keeps its visual
  size and pads its hit area to 44×44px below `md`. Icon size and hit size are
  independent.

---

## Brand assets

The logo is the most prominent visual element in the product, so it is
specified here for the same reason every colour and radius is: one authority.
An asset that enters the interface without a rule in this document opens a
second route for visual decisions, and the two routes disagree within a
release.

Brand assets are also the one part of this document that is **not** covered by
the code licence. See `LICENSING.md`: the code is AGPL-3.0-only and MIT, the
name and these marks are not.

### The assets

| Asset | File | Intrinsic size | Use |
| --- | --- | --- | --- |
| Wordmark | `.github/assets/logo.png` | 1024 × 414 (2.473 : 1) | README, repository social preview, documentation headers |
| Lettermark | `web/src/assets/logo-letter.png` | 889 × 1024 (0.868 : 1) | Application chrome — navigation, sign-in, empty states, favicon source |

Both are RGBA with genuine transparency: the interior is transparent and the
mark carries its own outer frame. Neither has padding beyond that frame, so
clear space must be applied by whatever places them.

### Minimum sizes

Below these the letterforms lose their counters and the mark reads as a smudge,
which is the same reasoning that puts a 1.5px stroke on a 16px icon.

| Asset | Minimum | Maximum in a README | Retina headroom at the minimum |
| --- | --- | --- | --- |
| Wordmark | **160px wide** | 400px wide | 6.4× |
| Lettermark | **24px tall** | — | 42× |

Both files clear 2× at every size named above by a wide margin. Anything
rendered above **512px wide** (wordmark) or **512px tall** (lettermark) is past
2× of the intrinsic file and will soften; use the vector once it exists rather
than upscaling.

### Clear space

**25% of the asset's rendered height, on all four sides, measured outside the
frame the asset already carries.** A 32px lettermark therefore reserves 8px of
empty space around it.

Nothing enters that space — no text, no border, no adjacent control, no edge of
a container. The clear space is not padding that a tight layout may borrow.

### Surfaces, and the variant question

**This is unresolved, and the current assets do not satisfy it.**

§8.2 of `CLAUDE.md` sets 3:1 for "icons and marks that carry meaning", and a
logo is the clearest case of one. Measured against every surface Nusa ships,
using the extremes of the mark's own gradient:

| Surface | Darkest ink `#FF46FF` | Lightest ink `#FFC3FF` | Verdict |
| --- | --- | --- | --- |
| `--surface-default` `#ffffff` | 2.77 : 1 | **1.45 : 1** | fails |
| `--surface-base` `#f8fafc` | 2.65 : 1 | **1.38 : 1** | fails |
| `--surface-sunken` `#f1f5f9` | 2.53 : 1 | **1.32 : 1** | fails |
| `--surface-inverse` `#0f172a` | 6.44 : 1 | 12.33 : 1 | passes |

The marks are legible only on the inverse surface, which is a button fill
rather than a page background. On the three light surfaces that are the entire
shipped palette, the mark fades out from its own gradient downward.

The saturated magenta is also in direct tension with Do's and Don'ts item 4 —
*don't introduce harsh neons or saturated accent colours* — and appears nowhere
in the palette in §Colors.

Two resolutions, and this document must record which was chosen:

1. **Recolour the mark** so its lightest value clears 3:1 against `#ffffff`,
   `#f8fafc` and `#f1f5f9`. On white that means a relative luminance at or
   below roughly 0.29 — in practice a mark drawn from the existing palette
   rather than beside it. One asset, every surface, no variants.
2. **Keep the magenta and ship two variants**, with the current light-ink mark
   used *only* on `--surface-inverse` or another dark plate, and a dark-ink
   variant for the three light surfaces. Two files to keep in step, and every
   placement then has to know which surface it is on.

Until one is chosen, the two marks are treated differently, and the line
between them is where §8.2 actually binds:

- **The wordmark is in the README masthead.** That is brand presentation on
  GitHub's surface rather than a control on one of ours, it sits beside the
  name in text, and it is one line to change. The mark is legible there — the
  saturated top of each letter carries at 2.77:1 — it is simply washed out
  toward the bottom of its gradient.
- **The lettermark is not placed in the application.** Application chrome is an
  interface element on Nusa's own specified surfaces, which is exactly where
  the 3:1 floor applies without argument. Nothing imports it yet.

> **A note on the standard applied.** WCAG exempts logotypes from contrast
> requirements, so the floor above is stricter than the letter of 1.4.11, and
> §8.2 states its threshold without a logotype carve-out. Being exact about
> what was measured: the mark is not invisible on white. The darkest part of
> its gradient reads at 2.77:1 and the lightest at 1.45:1, so the top of each
> letterform carries and the bottom fades away. That is a mark being washed
> out, not a mark being absent — and it is still under the floor.

### Not covered by any automated guard

`npm run test:contrast` recomputes pairings from `tokens.css`, and
`npm run lint:tokens` scans `.ts`, `.tsx`, `.css`, `.html` and `.js` under
`src`. Neither reads an image. **The table above was measured by hand and will
not be rechecked by CI**, so a change to a brand asset is a change nothing
verifies — re-measure it here when an asset is replaced.

### Format: PNG is interim, SVG is preferred

Both marks are PNG today, and both should become SVG.

The reason is not file size. A vector removes the minimum-size table's
headroom column entirely, because there is no intrinsic resolution to run out
of, and it removes the variant problem in the ordinary case: a mark drawn with
`fill="currentColor"` takes its colour from the surface it sits on, so light
and dark stop being two files that can drift apart. It also survives the
favicon and PWA icon sizes, which a 889 × 1024 raster cannot supply as a square
without either padding or distortion.

Until then the PNGs are the source of truth, are marked `binary` in
`.gitattributes` so end-of-line normalisation never touches them, and are
committed as the exact bytes exported.

### Never

- **Never stretch or squash.** Both marks scale on one axis only if the other
  follows. Their aspect ratios are 2.473 : 1 and 0.868 : 1.
- **Never recolour** a mark in CSS, in a filter, or by editing the file, other
  than through the variant decision recorded above.
- **Never apply effects** — no drop shadow, no glow, no outline, no gradient
  overlay, no blend mode. The elevation system does not reach the logo.
- **Never rotate, skew or mirror.** The lettermark is not mirrored under RTL:
  §Icons mirrors directional glyphs, and a brand mark is not directional.
- **Never place a mark on a photograph, a gradient, or any translucent surface
  whose backdrop can change** — the same rule §8.2 applies to text, for the
  same reason: contrast that depends on what happens to be behind it cannot be
  verified once.
- **Never redraw, re-space or re-letter** the mark to fit a layout. Change the
  layout.
- **Never use the wordmark where the lettermark belongs**, or the reverse. The
  wordmark carries the name; the lettermark stands in for it where the name is
  already present or where the space is square.

### The name is a separate thing from the mark

Nothing here licenses the product name to appear in the interface as an image.
Every user-facing string goes through i18n (§9 of `CLAUDE.md`), and a wordmark
is not a substitute for a text label: anywhere the wordmark appears it carries
a meaningful `alt`, and anywhere the name must be *read* — a page title, a
heading, a screen reader, a terminal — it is text.

---

## Components

Every interactive component below satisfies the control-identification rule:
each carries a fill that differs from its surroundings, a permanently visible
label, or an icon — so no control rests on its border alone.

### Buttons

**Variants**

| Variant | Fill | Text | Border | Hover fill |
| --- | --- | --- | --- | --- |
| Primary | `#0F172A` | `#FFFFFF` | none | `#020617` |
| Secondary | transparent | `#0F172A` | 1px `#0F172A` | `#0F172A0A` |
| Ghost | transparent | `--text-muted` | none | `--surface-sunken` |
| Destructive | `--status-error-content` | `#FFFFFF` | none | `#991B1B` |

The destructive button fills with the error *content* value rather than the
fill value. White text on the `#EF4444` fill reaches only 3.76:1; on
`#B91C1C` it reaches 6.47:1. This is the two-role rule applied to a surface
that carries a label.

**Sizes**

| Size | Padding | Font | Height |
| --- | --- | --- | --- |
| sm | 6px 14px | 14px | 32px |
| md | 10px 22px | 14px | 42px |
| lg | 12px 28px | 16px | 48px |

Below `md` every button meets the 44px touch minimum; `sm` and `md` buttons
gain hit-area padding rather than growing visually.

---

### Cards

| Variant | Fill | Border | Shadow | Radius |
| --- | --- | --- | --- | --- |
| Default | `--surface-default` | 1px `--border-subtle` | none | 8px |
| Elevated | `--surface-default` | none | `md` | 8px |

- **Padding**: 24px
- **Image area**: top slot, radius 8px 8px 0 0
- **Header bar**: optional tinted strip in `--surface-inverse` with
  `--text-on-inverse` text, for category labels

---

### Inputs

| State | Border | Fill | Shadow |
| --- | --- | --- | --- |
| Default | 1px `--border-subtle` | `--surface-default` | none |
| Hover | 1px `#0F172A` | `--surface-default` | none |
| Focus | 2px `#0F172A` | `--surface-default` | 3px ring `#0F172A18` |
| Error | 2px `--status-error-content` | `--surface-default` | 3px ring `#B91C1C18` |
| Disabled | 1px `--border-subtle` | `--surface-sunken` | none |

- **Height**: 42px · **Padding**: 10px 14px · **Radius**: 8px
- **Label**: DM Sans 14px/500, `--text-primary`, 6px bottom margin — **always
  present and always visible**
- **Helper text**: DM Sans 12px/400, `--text-muted`, 4px top margin
- **Error text**: DM Sans 12px/400, `--status-error-content`, 4px top margin

The visible label is what identifies the field, and it is mandatory for that
reason. An input on a white card has a fill identical to its background, so its
border is doing no identifying work; the label is the control's real marker.
Placeholder text is not a label — it disappears exactly when the user needs it,
and it is never a substitute.

Error is never signalled by border colour alone: the error text is always
present, and the field is associated with it for assistive technology.

Amount inputs use tabular figures and align their value to the inline end.

---

### Chips

| Variant | Fill | Text | Border |
| --- | --- | --- | --- |
| Filter | `--surface-base` | `--text-primary` | 1px `--border-strong` |
| Filter Active | `#0F172A` | `#FFFFFF` | none |
| Status Success | `--status-success-surface` | `--status-success-content` | none |
| Status Warning | `--status-warning-surface` | `--status-warning-content` | none |
| Status Error | `--status-error-surface` | `--status-error-content` | none |
| Status Info | `--status-info-surface` | `--status-info-content` | none |

- **Padding**: 4px 12px · **Radius**: 4px
- **Font**: 12px/500, uppercase, 0.5px tracking
- **Border on Filter chips**: `--border-strong`

A Filter chip's `--surface-base` fill is the same colour as the page
background, so on a page — as opposed to on a card — its body is invisible and
only the border marks it. Its text label satisfies the identification rule, but
the border is upgraded to `--border-strong` so the chip still reads as a
container rather than as loose text.

Uppercase applies to the Latin script only; it is a styling choice that some
scripts do not have, and it is never applied by transforming the underlying
string.

---

### Lists

- **Row height**: 48px (56px below `md`)
- **Padding**: 8px 16px
- **Divider**: 1px `--divider`
- **Hover background**: `--surface-base`
- **Active background**: `#0F172A06`
- **Label**: DM Sans 16px/400
- **Description**: DM Sans 14px/400, `--text-muted`

---

### Checkboxes

- **Size**: 18px × 18px · **Radius**: 4px
- **Unchecked**: 1.5px `--border-strong` border, `--surface-default` fill
- **Checked**: `#0F172A` fill, no border, `#FFFFFF` checkmark
- **Indeterminate**: `#0F172A` fill, `#FFFFFF` dash
- **Disabled**: 40% opacity, `cursor: not-allowed`
- **Label spacing**: 8px on the inline-start side of the label
- **Hit area**: 44×44px below `md`

An unchecked checkbox has a fill identical to the card behind it, so the
**visible label is mandatory** — it is the control's second indicator and the
only one that survives when the box itself is faint. A checkbox with no visible
label is not permitted; use an icon button instead.

---

### Radio Buttons

- **Size**: 18px × 18px · **Radius**: full
- **Unchecked**: 1.5px `--border-strong` border, `--surface-default` fill
- **Selected**: 2px `#0F172A` border, 8px `#0F172A` inner dot
- **Disabled**: 40% opacity, `cursor: not-allowed`
- **Label spacing**: 8px on the inline-start side of the label
- **Hit area**: 44×44px below `md`

As with checkboxes, the visible label is mandatory and is the control's second
indicator.

---

### Tooltips

- **Background**: `--surface-inverse`
- **Text**: `--text-on-inverse`, DM Sans 12px/400
- **Padding**: 6px 12px · **Radius**: 8px
- **Arrow**: 6px triangle matching the background
- **Max width**: 240px
- **Delay**: 150ms show, 0ms hide

A tooltip never carries information available nowhere else, because it is
unreachable by touch.

---

### Top bar

The bar across the top of every screen. Below `md` it is the only persistent
chrome above the content, because navigation has moved to a bottom bar; at `md`
and above it sits beside a persistent sidebar.

| Property | Below `md` | `md` and above |
| --- | --- | --- |
| Height | **minimum** 56px | **minimum** 64px |
| Padding inline | 16px | 24px |
| Fill | `--surface-default` | `--surface-default` |
| Border block-end | 1px `--border-subtle` | 1px `--border-subtle` |
| Content width | capped at `--measure-shell` | capped at `--measure-shell` |

56px is the same figure as a list row below `md`, so the bar comes off the
scale the rest of the product already uses rather than introducing a height of
its own.

**The height is a minimum, never a fixed value.** The page title wraps to a
second line and the bar grows with it. It is never ellipsised: § Internationalization
makes an important label something that wraps, and a page title is the most
important label on the screen. A bar with a locked height forces the ellipsis,
and Indonesian runs 15–20% longer than the English the layout was first seen
in.

**Contents.**

- **Inline-start: the page title.** H3 below `md`, H2 at `md` and above, in
  `--text-primary`.
- **Inline-end: at most two icon actions.** 24px icons in `--text-muted`, each
  with an `aria-label`, each padded to a 44×44px hit area below `md`.

**Two is the ceiling, and it is a real constraint rather than a preference.** A
third action competes with the title for a 360px line. A screen that needs more
needs an overflow menu, and an overflow menu needs a component this system does
not yet specify — so the screen waits rather than the bar getting crowded.

Destructive and rarely used actions belong here, per § Responsiveness: the top
of the viewport is hard to reach with a thumb, which is exactly what those
actions want.

**No wordmark.** The wordmark lives in the sidebar at `md` and above, and below
`md` it appears in application chrome not at all. On a 360px screen the scarcest
thing is horizontal space and the most valuable information is *where am I*,
not *which application is this* — the reader opened it a moment ago. The product
name stays reachable as text elsewhere, which is what § The name is a separate
thing from the mark requires anyway.

**Sticky.** The bar sticks to the top of the scrolling container and gains the
`sm` elevation once content scrolls beneath it — the same treatment § Dense data
patterns gives a sticky table header, for the same reason.

**Safe area.** The bar is full-bleed and adds `env(safe-area-inset-top)` to its
block-start padding. The inset region is painted `--surface-default` too, so a
notch never reveals the page colour running behind the bar.

---

### Empty states

`CLAUDE.md` §7 makes these normative: every empty state teaches something and
offers one action. This is the shape that satisfies it.

**Anatomy — four parts, in this order.**

| # | Part | Specification |
| --- | --- | --- |
| 1 | Icon | 32px, `--text-secondary`, `aria-hidden` |
| 2 | Heading | H3, `--text-primary` |
| 3 | Explanation | Body SM, `--text-muted`. At most two sentences, each at most 20 words |
| 4 | Action | Exactly one Primary button, size md |

The icon is `aria-hidden` because the heading already says the same thing in
words. That makes it decorative, so the two-role rule does not demand a CONTENT
value of it.

**Spacing.** Icon → heading 16px. Heading → explanation 8px. Explanation →
action 24px. Block padding 48px above and below when the empty state fills a
page region.

**Text column caps at `--measure-narrow` (44 characters)**, not at
`--measure-prose`. An empty state is centred, and centred text is markedly
harder to read as the line grows: the eye has to hunt for the start of each
line rather than returning to a fixed margin.

**One `<Explain>` is permitted inside the explanation, and it is not a second
action.** It is an inline clarifier on a term, which is what lets an empty state
teach something without the explanation growing past two sentences.

**Two things an empty state is not.**

**Never shown while data is loading.** "You have no transactions yet" during a
fetch is a false statement about someone's money. Empty is a conclusion;
loading is the absence of one. The only permitted progression is
skeleton → (empty | content), never empty → content.

**Never shown for a failed request.** Empty means there is nothing; failed
means we do not know. A failure carries the error message pattern — with an
icon and a word, never colour alone — because this pattern offers an action
that says *create the first one*, and offering that after a request failed
invites someone to duplicate something that may already exist.

---

### Skeletons

§ Component states requires a skeleton to be sized to the content it replaces.
The geometry below is what makes that checkable rather than a good intention.

| Property | Value |
| --- | --- |
| Fill | `--skeleton-fill` |
| Radius, text lines | `--radius-sm` (4px) |
| Radius, block shapes | the radius of the element being replaced — a card 8px, an avatar full |

`--skeleton-fill` carries the same value as `--surface-sunken` today and is a
separate token on purpose. A skeleton is not a sunken surface; it merely happens
to be the same colour in this theme. Tokens are named for what they do, and a
dark theme is likely to move the two in different directions — a shared name
would force them to move together.

**The painted bar is not the line box.** The skeleton's line box matches the
line box of the text it replaces exactly, which is what stops the layout
shifting. The bar painted inside it is `--skeleton-bar-scale` (0.7) of the font
size, centred vertically in that box. Body text at 16px/1.6 therefore occupies
a 25.6px box and paints an 11px bar.

A bar filling the whole line box lays down far more ink than the letterforms
that will replace it, so the block reads darker than the text does — and the
page then appears to lighten as data arrives, which reads as a change that did
not happen.

**Line widths.**

- The **last line** of a multi-line text skeleton is 60% width. Real paragraphs
  end mid-line, and a stack of equal-width bars reads as a table rather than as
  prose.
- A **numeric column** is always full width and aligned to the inline end,
  never 60%. The column's width is fixed and a short bar misrepresents the
  alignment, which is most of what a numeric column is for.

**Line count.** Exactly the number of lines the real content will have. Where
that is not known in advance — a list — the skeleton renders as many rows as the
page size the query is about to request, so the first page arriving shifts
nothing.

**Motion: a pulse, never a shimmer.** A shimmer is a travelling gradient, and
§ Accessibility floor forbids gradients inside table cells, which is where
skeletons most often appear here. Instead the skeleton pulses its opacity
between 1 and 0.6 over `--motion-pulse` (1200ms) on `--ease-standard`,
alternating.

Under `prefers-reduced-motion: reduce` or `data-effects="reduced"` the pulse
stops and the skeleton is a static fill. Nothing is lost: a skeleton's meaning
is its shape, not its movement.

**Accessibility.** The container carries `aria-busy="true"` and the bars
themselves are `aria-hidden`. A single visually hidden live region announces
loading once. A screen reader must never read out forty empty elements.

**A region is entirely skeleton or entirely real, never a mixture.** A
half-loaded region makes the page appear to load twice, and both of those
shifts are the thing a skeleton exists to prevent.

---

## Internationalization and text length

English is the shortest language Nusa will ever render. Indonesian runs roughly
15–20% longer for the same message, and other languages run considerably longer
still.

- **Never size a container to its English text.** Buttons, labels, table
  headers and navigation items size to their content, with a minimum rather
  than a fixed width.
- **Important labels wrap; they do not ellipsise.** A truncated envelope name
  or account name is a label that failed at its only job. Ellipsis is reserved
  for genuinely secondary text where the full value is available elsewhere, and
  never for numbers.
- **Layouts use logical CSS properties** — `margin-inline-start`,
  `padding-inline-end`, `border-inline-start`, `inset-inline-start` — never
  their physical equivalents. Full RTL support is planned, and logical
  properties from the start are what keep that a translation task rather than a
  rebuild.
- **Icons that indicate direction** are mirrored under RTL; icons that
  represent objects are not.
- **Never concatenate translated fragments.** Full sentences with
  interpolation, per `CLAUDE.md` §9.

---

## Accessibility floor

These are verified mechanically, not by eye. `npm run test:contrast` reads the
generated tokens, checks every pairing, and fails the build below threshold.

Thresholds differ by role, because one uniform number produces both false
failures and false passes:

| Role | Threshold | Basis |
| --- | --- | --- |
| Normal text (< 18.66px bold, < 24px) | **4.5:1** | WCAG 1.4.3 |
| Large text (≥ 18.66px bold or ≥ 24px) | **3:1** | WCAG 1.4.3 |
| Meaning-bearing icons and marks | **3:1** | WCAG 1.4.11 |
| A boundary that is a control's sole indicator | **3:1** | WCAG 1.4.11 |
| Focus indicators | **3:1** | WCAG 1.4.11, unconditional |
| Decorative borders and dividers | none | Not an indicator |

Alongside the numbers:

- Colour is never the sole carrier of meaning. Every colour-coded state also
  carries an icon, a sign, or a word.
- Text never sits on a translucent surface whose backdrop can change.
- No gradients inside table cells.
- Every interactive control carries an indicator besides its border.

---

## Do's and Don'ts

1. **Do** use the Navy + White contrast as the primary visual rhythm; Sage
   green is reserved for interactive elements and positive states only.
2. **Do** lean on generous whitespace and breathing room — a financial
   interface should never feel cramped.
3. **Do** use the softer 8px radius consistently; rounded corners convey
   approachability and calm.
4. **Don't** introduce harsh neons or saturated accent colours — Nusa is
   calming and precise.
5. **Don't** use condensed or decorative fonts; Plus Jakarta Sans and DM Sans
   are chosen for legibility at all sizes.
6. **Do** use uppercase chip labels with tracking for a polished, exact feel.
7. **Don't** allow *unstructured* density. A transaction register is meant to
   be dense; give it row rhythm, sticky headers, aligned numeric columns and
   clear grouping, then let it be dense. Reach for progressive disclosure and
   collapsible sections to organise density, not to hide the data someone came
   to read.
8. **Do** include clear iconography alongside text labels, and use the CONTENT
   value for any icon that carries meaning.
9. **Don't** use heavy drop shadows; the diffused elevation system maintains
   the clean, precise aesthetic.
10. **Do** render every balance, transaction amount and portfolio value with
    tabular figures, right-aligned, at a consistent decimal count.

---
