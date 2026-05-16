SET search_path TO axiaops;

ALTER POLICY zombie_snapshot_services_tenant_isolation ON zombie_snapshot_services RENAME TO snapshot_services_tenant_isolation;
ALTER POLICY dismissed_zombies_tenant_isolation ON dismissed_zombies RENAME TO dismissed_ghosts_tenant_isolation;
ALTER POLICY zombie_snapshots_tenant_isolation ON zombie_snapshots RENAME TO ghost_snapshots_tenant_isolation;
ALTER POLICY zombie_tenant_isolation ON zombie_records RENAME TO ghost_tenant_isolation;

ALTER INDEX dismissed_zombies_expiry RENAME TO dismissed_ghosts_expiry;
ALTER INDEX dismissed_zombies_active_fingerprint RENAME TO dismissed_ghosts_active_fingerprint;

ALTER INDEX dismissed_zombies_pkey RENAME TO dismissed_ghosts_pkey;
ALTER INDEX zombie_snapshot_services_pkey RENAME TO ghost_snapshot_services_pkey;
ALTER INDEX zombie_snapshots_pkey RENAME TO ghost_snapshots_pkey;
ALTER INDEX zombie_records_pkey RENAME TO ghost_records_pkey;

ALTER SEQUENCE dismissed_zombies_id_seq RENAME TO dismissed_ghosts_id_seq;
ALTER SEQUENCE zombie_records_id_seq RENAME TO ghost_records_id_seq;

ALTER TABLE resource_records RENAME COLUMN is_zombie TO is_ghost;
ALTER TABLE zombie_snapshot_services RENAME COLUMN zombie_count TO ghost_count;
ALTER TABLE zombie_snapshots RENAME COLUMN zombie_count TO ghost_count;

ALTER TABLE dismissed_zombies RENAME TO dismissed_ghosts;
ALTER TABLE zombie_snapshot_services RENAME TO ghost_snapshot_services;
ALTER TABLE zombie_snapshots RENAME TO ghost_snapshots;
ALTER TABLE zombie_records RENAME TO ghost_records;
