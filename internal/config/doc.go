// SPDX-License-Identifier: AGPL-3.0-only

// Package config reads and validates configuration from the environment.
//
// Configuration is resolved once, at startup, into a typed struct. Every
// problem found is reported together so an operator fixes one round of errors
// rather than discovering them one restart at a time. A misconfigured process
// exits before it starts serving; nothing here panics halfway through a
// request.
//
// This package is deliberately absent from the architecture diagram's list of
// domain packages: it sits at the edge alongside cmd. Nothing in the domain may
// import it.
package config
