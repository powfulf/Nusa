-- name: ListCommodities :many
-- Every commodity this instance knows, core plus whatever Country Packs added.
SELECT code, kind, scale FROM commodities ORDER BY code;

-- name: GetCommodity :one
SELECT code, kind, scale FROM commodities WHERE code = $1;

-- name: UpsertCommodity :exec
-- Country Packs register their own securities and funds. A redefinition that
-- disagrees with what is already stored is refused rather than silently
-- applied: a commodity whose scale changes underneath stored amounts would
-- move every decimal point already written.
INSERT INTO commodities (code, kind, scale)
VALUES ($1, $2, $3)
ON CONFLICT (code) DO UPDATE
    SET kind = excluded.kind
  WHERE commodities.kind = excluded.kind
    AND commodities.scale = excluded.scale;
