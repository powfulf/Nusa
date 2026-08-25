-- The reverse of 000008, in the reverse order: the tables that point at users
-- go first, then the indexes and constraints added to users, then its columns.

DROP TABLE IF EXISTS user_backup_codes;
DROP TABLE IF EXISTS user_totp;
DROP TABLE IF EXISTS sessions;

-- Both of these are redundant and kept anyway. Each indexes an expression over
-- email, so dropping that column below removes them regardless — verified by
-- deleting these two lines and finding the catalogue comparison identical.
-- They stay because a down migration that mirrors its up is one a reader can
-- check line by line, and because the redundancy costs nothing while a future
-- index over a column that survives would not be removed for free.
DROP INDEX IF EXISTS users_at_most_one_credentialed;
DROP INDEX IF EXISTS users_email_key;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_password_changed_with_the_password,
    DROP CONSTRAINT IF EXISTS users_password_hash_is_argon2id,
    DROP CONSTRAINT IF EXISTS users_email_looks_like_an_address,
    DROP CONSTRAINT IF EXISTS users_email_is_not_blank,
    DROP CONSTRAINT IF EXISTS users_credentials_are_all_or_nothing;

ALTER TABLE users
    DROP COLUMN IF EXISTS password_changed_at,
    DROP COLUMN IF EXISTS password_hash,
    DROP COLUMN IF EXISTS email;

UPDATE schema_meta SET version = 7, applied_at = now();
