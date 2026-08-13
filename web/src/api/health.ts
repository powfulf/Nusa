/** Shape of the /healthz response. Mirrors internal/api/health.go. */
export interface Health {
  status: 'ok' | 'unavailable'
  service: string
  checks: {
    database: {
      status: 'ok' | 'unavailable'
      /** Absent when the check failed, so it is never confused with version 0. */
      schema_version?: number
    }
  }
}

/**
 * Fetches server health.
 *
 * A degraded server answers 503 with a body describing what is wrong, so the
 * body is read regardless of status. Only a transport failure throws.
 */
export async function fetchHealth(signal?: AbortSignal): Promise<Health> {
  const response = await fetch('/healthz', {
    signal,
    headers: { Accept: 'application/json' },
  })

  const contentType = response.headers.get('content-type') ?? ''
  if (!contentType.includes('application/json')) {
    throw new Error(`unexpected response type: ${response.status}`)
  }

  return (await response.json()) as Health
}
