// SPDX-License-Identifier: AGPL-3.0-only

// Package api is the HTTP boundary: routing, middleware, request and response
// DTOs, and the OpenAPI document generated from them.
//
// This layer is thin on purpose. It decodes, delegates to the domain, and
// encodes. Business rules do not live here — if a handler starts making
// decisions about money, those decisions belong in a domain package instead.
//
// Money crosses this boundary as a string amount in minor units paired with a
// commodity code, because JavaScript numbers lose precision above 2^53.
//
// Currently serves the health endpoint and the built frontend. The REST API
// arrives in milestone M2.
package api
