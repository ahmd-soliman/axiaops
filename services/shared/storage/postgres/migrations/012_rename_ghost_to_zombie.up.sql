SET search_path TO axiaops;

ALTER TABLE ghost_records RENAME TO zombie_records;
ALTER TABLE ghost_snapshots RENAME TO zombie_snapshots;
ALTER TABLE ghost_snapshot_services RENAME TO zombie_snapshot_services;
ALTER TABLE dismissed_ghosts RENAME TO dismissed_zombies;

ALTER TABLE zombie_snapshots RENAME COLUMN ghost_count TO zombie_count;
ALTER TABLE zombie_snapshot_services RENAME COLUMN ghost_count TO zombie_count;
ALTER TABLE resource_records RENAME COLUMN is_ghost TO is_zombie;

ALTER SEQUENCE ghost_records_id_seq RENAME TO zombie_records_id_seq;
ALTER SEQUENCE dismissed_ghosts_id_seq RENAME TO dismissed_zombies_id_seq;

ALTER INDEX ghost_records_pkey RENAME TO zombie_records_pkey;
ALTER INDEX ghost_snapshots_pkey RENAME TO zombie_snapshots_pkey;
ALTER INDEX ghost_snapshot_services_pkey RENAME TO zombie_snapshot_services_pkey;
ALTER INDEX dismissed_ghosts_pkey RENAME TO dismissed_zombies_pkey;

ALTER INDEX dismissed_ghosts_active_fingerprint RENAME TO dismissed_zombies_active_fingerprint;
ALTER INDEX dismissed_ghosts_expiry RENAME TO dismissed_zombies_expiry;

ALTER POLICY ghost_tenant_isolation ON zombie_records RENAME TO zombie_tenant_isolation;
ALTER POLICY ghost_snapshots_tenant_isolation ON zombie_snapshots RENAME TO zombie_snapshots_tenant_isolation;
ALTER POLICY dismissed_ghosts_tenant_isolation ON dismissed_zombies RENAME TO dismissed_zombies_tenant_isolation;
ALTER POLICY snapshot_services_tenant_isolation ON zombie_snapshot_services RENAME TO zombie_snapshot_services_tenant_isolation;
