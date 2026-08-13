# Security Policy

## Reporting a vulnerability

**Do not open a public issue for security problems.**

Report privately through GitHub's [private vulnerability
reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
on this repository.

Please include what the issue is, how to reproduce it, and what an attacker could
achieve with it. If you have a suggested fix, even better.

We aim to acknowledge reports within 72 hours and to ship a fix or a mitigation
plan within 30 days. We will credit you in the advisory unless you prefer
otherwise.

## Supported versions

The project is pre-alpha. Only the `main` branch receives fixes. Once there are
tagged releases, this section will list the supported ones.

## Scope

Nusa is self-hosted, so the operator of an instance controls its network exposure,
TLS termination and backups. Reports about the software itself are in scope:

- Authentication and session handling
- Authorization and data isolation between users and households
- Injection of any kind, including into Starlark plugin sandboxes
- Sandbox escapes from Country Pack plugins
- Leaking one user's financial data to another through any endpoint, including
  aggregate reports, exports, search and error messages

Out of scope: misconfigured deployments, missing hardening on an operator's own
server, and findings that require an already-compromised host.

## Design commitments

These are properties of the product, not implementation details. If you find
behaviour that contradicts one of them, treat it as a security issue:

- Nusa never asks for bank or broker credentials.
- Nusa sends no telemetry.
- Country Pack data providers run server-side and cached per instance, never per
  user, so a third party cannot observe an individual user's request patterns.
- Starlark plugins get no network access, no filesystem access, a deterministic
  injected clock, and bounded execution.
- Every mutation is recorded in an audit log with its actor and origin.
