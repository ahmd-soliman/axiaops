-- 030_normalize_cost_explorer_service_names.down.sql
-- Intentional no-op. The up migration normalises raw Cost Explorer display
-- names into internal service identifiers (e.g. "Amazon EC2 Container Registry
-- (ECR)" -> "AmazonECR"). It is not reversible: once renamed, a row that came
-- from the backfill is indistinguishable from one a post-migration scan wrote
-- under the same internal id, so reversing would corrupt legitimately-clean
-- rows. The normalisation is also the desired end state, so there is nothing
-- to roll back.

SET search_path TO axiaops;
