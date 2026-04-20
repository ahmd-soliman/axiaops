-- 005_resource_type.up.sql
-- Add resource_type column for two-tier service filtering (e.g. AmazonEC2 + volume/snapshot/ami).
-- Canonical AWS service names stay in `service`; sub-classification goes in `resource_type`.

SET search_path TO axiaops;

ALTER TABLE ghost_snapshot_services ADD COLUMN resource_type TEXT NOT NULL DEFAULT '';
ALTER TABLE ghost_records           ADD COLUMN resource_type TEXT NOT NULL DEFAULT '';
ALTER TABLE resource_records        ADD COLUMN resource_type TEXT NOT NULL DEFAULT '';

-- Composite index for filtered trend queries
CREATE INDEX IF NOT EXISTS idx_snapshot_services_tenant_service_rt
    ON ghost_snapshot_services (tenant_id, service, resource_type);

-- Migrate existing compound service names to canonical + resource_type
UPDATE ghost_snapshot_services SET service = 'AmazonEC2', resource_type = 'volume'           WHERE service = 'AmazonEC2-EBS';
UPDATE ghost_snapshot_services SET service = 'AmazonEC2', resource_type = 'snapshot'         WHERE service = 'AmazonEC2-Snapshots';
UPDATE ghost_snapshot_services SET service = 'AmazonEC2', resource_type = 'stopped_instance' WHERE service = 'AmazonEC2-Stopped';
UPDATE ghost_snapshot_services SET service = 'AmazonEC2', resource_type = 'ami'              WHERE service = 'AmazonEC2-AMI';

UPDATE ghost_records SET resource_type = 'volume'           WHERE service = 'AmazonEC2' AND usage_metric = 'VolumeState';
UPDATE ghost_records SET resource_type = 'snapshot'         WHERE service = 'AmazonEC2' AND usage_metric = 'SourceVolumeExists';
UPDATE ghost_records SET resource_type = 'stopped_instance' WHERE service = 'AmazonEC2' AND usage_metric = 'DaysStopped';
UPDATE ghost_records SET resource_type = 'ami'              WHERE service = 'AmazonEC2' AND usage_metric = 'DaysSinceCreation';
UPDATE ghost_records SET resource_type = 'instance'         WHERE service = 'AmazonEC2' AND usage_metric = 'CPUUtilization';

UPDATE resource_records SET resource_type = 'volume'           WHERE service = 'AmazonEC2' AND usage_metric = 'VolumeState';
UPDATE resource_records SET resource_type = 'snapshot'         WHERE service = 'AmazonEC2' AND usage_metric = 'SourceVolumeExists';
UPDATE resource_records SET resource_type = 'stopped_instance' WHERE service = 'AmazonEC2' AND usage_metric = 'DaysStopped';
UPDATE resource_records SET resource_type = 'ami'              WHERE service = 'AmazonEC2' AND usage_metric = 'DaysSinceCreation';
UPDATE resource_records SET resource_type = 'instance'         WHERE service = 'AmazonEC2' AND usage_metric = 'CPUUtilization';

-- VPC resource types
UPDATE ghost_records SET resource_type = 'eip' WHERE service = 'AmazonVPC' AND usage_metric = 'NetworkInterfaceAttachment';
UPDATE resource_records SET resource_type = 'eip' WHERE service = 'AmazonVPC' AND usage_metric = 'NetworkInterfaceAttachment';

-- RDS resource types (primary by default; detection would distinguish read replicas)
UPDATE ghost_records SET resource_type = 'primary' WHERE service = 'AmazonRDS';
UPDATE resource_records SET resource_type = 'primary' WHERE service = 'AmazonRDS';
