-- 006_resource_type_migrate_data.down.sql
-- Rollback: restore compound service names and clear resource_type.

SET search_path TO axiaops;

-- ── Restore ghost_snapshot_services ───────────────────────────────────────────

UPDATE ghost_snapshot_services
  SET service = 'AmazonEC2-EBS'
  WHERE service = 'AmazonEC2' AND resource_type = 'volume';

UPDATE ghost_snapshot_services
  SET service = 'AmazonEC2-Snapshots'
  WHERE service = 'AmazonEC2' AND resource_type = 'snapshot';

UPDATE ghost_snapshot_services
  SET service = 'AmazonEC2-Stopped'
  WHERE service = 'AmazonEC2' AND resource_type = 'stopped_instance';

UPDATE ghost_snapshot_services
  SET service = 'AmazonEC2-AMI'
  WHERE service = 'AmazonEC2' AND resource_type = 'ami';

-- ── Clear resource_type columns (leave them in place for down idempotence) ──────

UPDATE ghost_snapshot_services SET resource_type = '';
UPDATE ghost_records SET resource_type = '';
UPDATE resource_records SET resource_type = '';
