-- 030_normalize_cost_explorer_service_names.up.sql
-- Backfill cost_records rows whose `service` is a raw AWS Cost Explorer display
-- name into the internal identifier the rest of the system uses.
--
-- Cause: ingestion's normalizeService (services/ingestion/internal/provider/aws/
-- aws.go) maps Cost Explorer display names to internal identifiers via the
-- ceServiceToInternal map, and on a miss returns the display name verbatim. The
-- map was missing several services, so cost rows landed under the long CE name
-- (e.g. "Amazon EC2 Container Registry (ECR)") while the dashboard's
-- serviceConfig.js only keys on the internal id ("AmazonECR"). Symptoms: ugly
-- long chip labels, and split aggregation against the resource-level rows that
-- already used the internal id.
--
-- The same MR adds the missing map entries, so future scans write the internal
-- id. This migration renames the historical rows to match. It ships in lockstep
-- with the code change and the migrate task runs before the new code scans, so
-- old rows are renamed first and subsequent upserts land on the same key in
-- place — no duplicate rows.
--
-- Collision-safe + idempotent: the rename only fires where it would not violate
-- cost_records_org_resource_unique (organization_id, provider, account_id,
-- service, region, resource_id, period_start, period_end); any leaked row that
-- cannot be renamed because a correct row already occupies the key is a stale
-- duplicate and is dropped. Re-running matches nothing.
--
-- The mapping mirrors ceServiceToInternal exactly; names absent from a given
-- env's data are simple no-ops.

SET search_path TO axiaops;

WITH mapping(ce_name, internal) AS (
    VALUES
        ('Amazon CloudFront',                   'AmazonCloudFront'),
        ('Amazon Kinesis',                      'AmazonKinesis'),
        ('EC2 Container Registry (ECR)',        'AmazonECR'),
        ('Amazon EC2 Container Registry (ECR)', 'AmazonECR'),
        ('Amazon Elastic Container Service',    'AmazonECS')
)
UPDATE cost_records c
SET service = m.internal
FROM mapping m
WHERE c.service = m.ce_name
  AND NOT EXISTS (
        SELECT 1
        FROM cost_records c2
        WHERE c2.organization_id = c.organization_id
          AND c2.provider        = c.provider
          AND c2.account_id      = c.account_id
          AND c2.service         = m.internal
          AND c2.region          = c.region
          AND c2.resource_id     = c.resource_id
          AND c2.period_start    = c.period_start
          AND c2.period_end      = c.period_end
  );

-- Drop any leaked rows that survived the rename: a correct internal-id row
-- already holds their unique key, so the long-name row is a stale duplicate.
DELETE FROM cost_records
WHERE service IN (
    'Amazon CloudFront',
    'Amazon Kinesis',
    'EC2 Container Registry (ECR)',
    'Amazon EC2 Container Registry (ECR)',
    'Amazon Elastic Container Service'
);
