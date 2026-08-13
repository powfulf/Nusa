# Country Pack — Indonesia

Everything country-specific lives in a Country Pack. Nothing country-specific
belongs in core. This is the first pack, and it doubles as the reference
implementation.

A pack is a directory with a `manifest.yaml` declaring its capabilities:
category taxonomy, institutions, importers, price providers, data providers,
goal templates, holidays and retirement schemes. Most of a pack is declarative
YAML validated against a schema; Starlark is available as an escape hatch for
parsing that YAML cannot express.

This pack will carry the Indonesian category taxonomy, the list of banks and
e-wallets, data providers for BI Rate, inflation, fuel prices, exchange rates
and IHSG, goal templates such as emergency, hajj, umrah, wedding and education
funds, and the national holiday calendar used to predict bills.

The pack runtime and this pack's contents are built in milestone M8. The pack
specification is MIT licensed, separately from the AGPL-3.0 application, so
packs can be written and shared freely.
