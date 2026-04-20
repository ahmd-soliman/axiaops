-- 006_resource_type_migrate_data.up.sql
-- Step 2: Populate resource_type column with appropriate values.
-- Transforms compound service names (e.g., AmazonEC2-EBS) to canonical service + resource_type pairs.

SET search_path TO axiaops;

-- ── ghost_snapshot_services: Decompose compound service names ──────────────────

UPDATE ghost_snapshot_services
  SET service = 'AmazonEC2', resource_type = 'volume'
  WHERE service = 'AmazonEC2-EBS';

UPDATE ghost_snapshot_services
  SET service = 'AmazonEC2', resource_type = 'snapshot'
  WHERE service = 'AmazonEC2-Snapshots';

UPDATE ghost_snapshot_services
  SET service = 'AmazonEC2', resource_type = 'stopped_instance'
  WHERE service = 'AmazonEC2-Stopped';

UPDATE ghost_snapshot_services
  SET service = 'AmazonEC2', resource_type = 'ami'
  WHERE service = 'AmazonEC2-AMI';

-- ── ghost_records: Classify by usage metric ──────────────────────────────────

UPDATE ghost_records
  SET resource_type = 'volume'
  WHERE service = 'AmazonEC2' AND usage_metric = 'VolumeState';

UPDATE ghost_records
  SET resource_type = 'snapshot'
  WHERE service = 'AmazonEC2' AND usage_metric = 'SourceVolumeExists';

UPDATE ghost_records
  SET resource_type = 'stopped_instance'
  WHERE service = 'AmazonEC2' AND usage_metric = 'DaysStopped';

UPDATE ghost_records
  SET resource_type = 'ami'
  WHERE service = 'AmazonEC2' AND usage_metric = 'DaysSinceCreation';

UPDATE ghost_records
  SET resource_type = 'instance'
  WHERE service = 'AmazonEC2' AND usage_metric = 'CPUUtilization';

UPDATE ghost_records
  SET resource_type = 'eip'
  WHERE service = 'AmazonVPC' AND usage_metric = 'NetworkInterfaceAttachment';

UPDATE ghost_records
  SET resource_type = 'primary'
  WHERE service = 'AmazonRDS';

-- ── resource_records: Same classification ──────────────────────────────────────

UPDATE resource_records
  SET resource_type = 'volume'
  WHERE service = 'AmazonEC2' AND usage_metric = 'VolumeState';

UPDATE resource_records
  SET resource_type = 'snapshot'
  WHERE service = 'AmazonEC2' AND usage_metric = 'SourceVolumeExists';

UPDATE resource_records
  SET resource_type = 'stopped_instance'
  WHERE service = 'AmazonEC2' AND usage_metric = 'DaysStopped';

UPDATE resource_records
  SET resource_type = 'ami'
  WHERE service = 'AmazonEC2' AND usage_metric = 'DaysSinceCreation';

UPDATE resource_records
  SET resource_type = 'instance'
  WHERE service = 'AmazonEC2' AND usage_metric = 'CPUUtilization';

UPDATE resource_records
  SET resource_type = 'eip'
  WHERE service = 'AmazonVPC' AND usage_metric = 'NetworkInterfaceAttachment';

UPDATE resource_records
  SET resource_type = 'primary'
  WHERE service = 'AmazonRDS';
