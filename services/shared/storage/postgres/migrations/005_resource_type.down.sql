SET search_path TO axiaops;

-- Restore compound service names before dropping column
UPDATE ghost_snapshot_services SET service = 'AmazonEC2-EBS'       WHERE service = 'AmazonEC2' AND resource_type = 'volume';
UPDATE ghost_snapshot_services SET service = 'AmazonEC2-Snapshots' WHERE service = 'AmazonEC2' AND resource_type = 'snapshot';
UPDATE ghost_snapshot_services SET service = 'AmazonEC2-Stopped'   WHERE service = 'AmazonEC2' AND resource_type = 'stopped_instance';
UPDATE ghost_snapshot_services SET service = 'AmazonEC2-AMI'       WHERE service = 'AmazonEC2' AND resource_type = 'ami';

DROP INDEX IF EXISTS idx_snapshot_services_tenant_service_rt;

ALTER TABLE ghost_snapshot_services DROP COLUMN resource_type;
ALTER TABLE ghost_records           DROP COLUMN resource_type;
ALTER TABLE resource_records        DROP COLUMN resource_type;
