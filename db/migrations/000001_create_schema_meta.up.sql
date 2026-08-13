-- Records which schema generation this database carries.
--
-- golang-migrate already tracks the migration version it applied. This table
-- exists for a different reason: the application and its health endpoint need
-- to read a schema version over a normal connection, without depending on the
-- migration tool's private bookkeeping table.
CREATE TABLE schema_meta (
    -- Single-row enforcement. The primary key can only ever hold true, because
    -- the check rejects false and the column is NOT NULL by virtue of being a
    -- primary key. A second INSERT therefore violates the key.
    singleton  boolean     PRIMARY KEY DEFAULT true CHECK (singleton),
    version    integer     NOT NULL CHECK (version > 0),
    applied_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE schema_meta IS
    'Schema generation for this database. Exactly one row, enforced by the singleton primary key.';

INSERT INTO schema_meta (version) VALUES (1);
