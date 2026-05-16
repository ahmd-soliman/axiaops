-- 013_kinde_sub_dev_prefix.up.sql
-- Formalise the "dev:" synthetic kinde_sub convention used by EnsureUser
-- (services/shared/storage/postgres/postgres.go). Any user row whose kinde_sub
-- starts with 'dev:' must have that prefix followed by the row's own id —
-- this prevents future code paths from producing colliding synthetic subs and
-- guarantees that DevBypass-injected user_ids round-trip cleanly.

SET search_path TO axiaops;

ALTER TABLE users
    ADD CONSTRAINT users_dev_kinde_sub_matches_id
    CHECK (
        kinde_sub NOT LIKE 'dev:%'
        OR kinde_sub = 'dev:' || id
    );
