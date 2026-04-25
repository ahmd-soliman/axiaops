# Custodian Rule-Mining Backlog

**Status:** M1 complete (per-service file split — branch `chore/refactor-discover-by-service`). M2–M5 pending.

## Why

AxiaOps's zombie-detection rule set is solid but narrow (~20 services). [Cloud Custodian](https://github.com/cloud-custodian/cloud-custodian) (Apache-2.0) has years of practitioner-hardened FinOps filters in `c7n/resources/*.py` that we don't have.

**We are not adopting Custodian as a runtime dependency.** Wrong runtime (Python), wrong execution model (per-account Lambda or cron), no multi-tenant primitive — all conflict with our SaaS shape and €24–34/mo cost target. Instead, we mine its filters as documentation and reimplement the *ideas* in Go, slotting into the per-service `discover_*.go` pattern established by M1.

License: Custodian is Apache-2.0; reimplementing detection logic doesn't require attribution, but each new function should carry a comment like `// Inspired by c7n/resources/<file>.py: <FilterClass>`.

## Backlog

Priorities: **P0** = high $-impact, mechanically straightforward; **P1** = medium impact, more API surface; **P2** = nice-to-have, lower frequency or complex.

### P0

| # | Service / Resource | Method | Custodian source | Est. waste/unit/mo | Edge cases |
|---|---|---|---|---|---|
| 1 | EC2 / SecurityGroup | API: `ec2:DescribeSecurityGroups` + `DescribeNetworkInterfaces` + cross-ref other SGs' `IpPermissions` for `UserIdGroupPairs`. **Non-obvious:** "unused" ≠ "no ENIs" — must also check no other SG references it | `vpc.py`: `UnusedSecurityGroup` | $0 (hygiene + audit) | Skip default VPC SG; skip SGs referenced by ELB/RDS even if no ENI |
| 2 | ELBv2 / TargetGroup | API: `elbv2:DescribeTargetGroups` + `DescribeTargetHealth` | `appelb.py`: `EmptyTargetGroup` | $0 direct, surfaces forgotten ALB listeners ($16/mo each) | Skip TGs created < 7 days |
| 3 | RDS / DBInstance overprovisioned | CW: `CPUUtilization` p95 < 20% AND `FreeableMemory` > 50% over 14d | `rds.py`: metrics filter | $30–500/mo | Skip Multi-AZ production-tagged; skip read replicas. **Design needed:** sizing recommendation requires new fields (`recommended_instance_class`, `monthly_savings_estimate`) on `ZombieResource` |
| 4 | ElastiCache / ReplicationGroup | CW: `CurrConnections=0` AND `NetworkBytesIn=0` over 7d | `elasticache.py`: `IdleReplicationGroup` | $50–800/mo | We detect single nodes today; extend to cluster-mode replication groups |
| 5 | OpenSearch / Domain (tightened) | CW: `SearchRate=0` AND `IndexingRate=0` over 14d | `elasticsearch.py`: metrics filter | $50–1,500/mo | Skip domains created < 14d. Tightens current rule by also checking IndexingRate |
| 6 | SageMaker / Endpoint (tightened) | Already partial; also flag endpoints with `InstanceCount > 1` and `InvocationsPerInstance < 10/day` | `sagemaker.py`: `EndpointConfigFilter` | $50–1,000/mo | Skip serverless endpoints |

### P1

| # | Service / Resource | Method | Custodian source | Est. waste/unit/mo | Edge cases |
|---|---|---|---|---|---|
| 7 | EC2 / VpcEndpoint (interface) | API: `ec2:DescribeVpcEndpoints` + CW `BytesProcessed=0` per ENI | `vpc.py`: `VpcEndpoint` | $7.20/mo per AZ + data | Skip gateway endpoints ($0); only flag interface endpoints |
| 8 | EC2 / TransitGatewayAttachment | API: `ec2:DescribeTransitGatewayAttachments` + CW `BytesIn=0` over 14d | `vpc.py`: `TransitGatewayAttachmentsFilter` | $36/mo per attachment | Skip pending/deleting state |
| 9 | Redshift / Snapshot (manual, old) | API: `redshift:DescribeClusterSnapshots` filter `SnapshotType=manual`, age > 30d, source cluster gone | `redshift.py`: snapshot age filter | $0.024/GB-mo | Mirror RDS snapshot logic |
| 10 | DynamoDB / Table (provisioned, low) | CW: `ConsumedReadCapacityUnits / ProvisionedReadCapacityUnits < 5%` over 14d | `dynamodb.py`: `consumed-capacity` | $5–500/mo | Skip on-demand tables. Current rule only flags zero |
| 11 | Lambda / Provisioned Concurrency idle | API: `lambda:ListProvisionedConcurrencyConfigs` + CW `ProvisionedConcurrencyUtilization < 10%` over 7d | `lambda.py`: concurrency filter | $30–300/mo per 100 PCU | Skip if PCU = 1 (cold-start mitigation) |
| 12 | NAT Gateway "abandoned" (route-table check) | API: `ec2:DescribeRouteTables` — flag NATs not referenced by any route. Strengthens current bytes=0 rule | `vpc.py`: nat-gateway route filter | $33/mo + data | Combine with existing CW rule |
| 13 | IAM / AccessKey (unused) | API: `iam:ListUsers` + `ListAccessKeys` + `GetAccessKeyLastUsed`. Flag `LastUsedDate` > 90d | `iam.py`: access-key `LastUsedDate` | $0 (security hygiene; correlates with abandoned automation) | Skip never-used keys < 7d old |

### P2 (long tail)

| # | Service / Resource | Method | Custodian source | Est. waste/unit/mo | Edge cases |
|---|---|---|---|---|---|
| 14 | GlobalAccelerator / Accelerator | API: `globalaccelerator:ListAccelerators` + CW `ProcessedBytes=0` over 14d | `globalaccelerator.py` | $18/mo fixed + endpoints | Skip ENABLED with healthy endpoints created < 14d |
| 15 | StepFunctions / StateMachine | API: `sfn:ListStateMachines` + CW `ExecutionsStarted=0` over 30d | `stepfunctions.py`: state-machine | $0 idle (surfaces abandoned automation + IAM) | Express workflows: separate metric |
| 16 | CodeBuild / Project | API: `codebuild:ListProjects` + `BatchGetBuilds` last build > 90d | `codebuild.py`: project | $0 idle (per-build pricing); surfaces stale CI | — |
| 17 | EBS / Snapshot copied across regions | API: extend snapshot logic to flag cross-region copies of deleted source | `ebs.py`: snapshot-cross-account | $0.05/GB-mo | Already partially covered |
| 18 | RDS / Cluster (Aurora) idle | CW: `DatabaseConnections=0` on cluster (not just instance) | `rdscluster.py` | $50+/mo | Cluster metrics differ from instance |
| 19 | KMS / Customer-managed key (unused) | API: `kms:ListKeys` + CloudTrail Lookup last use > 365d | `kms.py`: unused | $1/key-mo | Heavy CloudTrail lookup — gate behind opt-in |
| 20 | EFS / FileSystem (idle) | CW: `ClientConnections=0` over 14d AND no recent IOPS | `efs.py`: metrics | $0.30/GB-mo | EFS-IA tier reclassify suggestion |

## Non-obvious logic — design discussion before coding

- **#1 Security Groups:** "unused" requires (a) ENI attachments, (b) other SGs' inbound rules referencing this SG ID, (c) RDS/ELB/Lambda implicit references. Custodian builds a global SG-reference graph — we'll need the same in a single pass to keep API calls bounded.
- **#3 Overprovisioned RDS:** unlike binary "idle/not-idle" rules, this is a sizing recommendation. Needs new fields on `ZombieResource` (`recommended_instance_class`, `monthly_savings_estimate`) or a sibling table. **Design before implementation.**
- **#19 KMS unused keys:** requires CloudTrail Lookup, expensive/heavy. Gate behind opt-in.

## Per-rule implementation template

For each new rule, the contributor touches these files (paths absolute from repo root):

1. **Discovery code** → new function in the appropriate per-service file:
   `services/ingestion/internal/provider/aws/discover_<service>.go`
   - CloudWatch-threshold rule: add `discover<Service>()` returning `[]string` of resource IDs and a `case` in the `DiscoverResources` switch in `discover.go`.
   - API-only rule: add `func Discover<Verb><Resource>(ctx, records, awsClient, start, end, internalAccountID) ([]model.ZombieResource, error)`.
2. **Wire into worker** → `services/ingestion/cmd/main.go`. New block per rule, identical to the existing 12 blocks (~lines 467–599).
3. **Analyzer entry (CloudWatch rules only)** → add to `serviceRules` in `services/shared/analyzer/rules.go`. Add CloudWatch metric mapping in `services/ingestion/internal/provider/aws/cloudwatch.go`.
4. **Tests:**
   - Pure threshold logic → `services/shared/analyzer/detector_test.go`
   - AWS SDK mocking → new `discover_<service>_test.go` next to the new file
5. **IAM permissions** → append new `Action` strings to JSON block in `docs/production.md` (~lines 39–60).
6. **Detection-rules table** → update appropriate table in root `CLAUDE.md` (CloudWatch table ~line 82, API-only table ~line 96).
7. **Service mapping (if new service)** → add to `ceServiceToInternal` in `services/ingestion/internal/provider/aws/aws.go` (~lines 198–222).

### Worked example: unused security groups (rule #1)

- **New file:** `discover_securitygroups.go`
- **Function signature:**
  ```go
  func DiscoverUnusedSecurityGroups(
      ctx context.Context, records []model.CostRecord, awsClient *Client,
      start, end time.Time, internalAccountID string,
  ) ([]model.ZombieResource, error)
  ```
- **Logic:** for each region in `uniqueRegions(records)`, call `ec2:DescribeSecurityGroups` and `ec2:DescribeNetworkInterfaces`. Build `referencedSGIDs` from every other SG's `IpPermissions[*].UserIdGroupPairs[*].GroupId`. A SG is unused iff (id ∉ ENI groups) ∧ (id ∉ referencedSGIDs) ∧ (`GroupName != "default"`). Header: `// Inspired by c7n/resources/vpc.py: UnusedSecurityGroup`.
- **`MonthlyCost`:** `0.0`. `Reason = "Security group has no ENI attachments and is not referenced by any other SG — safe to remove"`.
- **Wire-in:** new block in `cmd/main.go` mirroring the EIP block.
- **`serviceRules`:** none — API-only rule.
- **IAM:** add `ec2:DescribeSecurityGroups`, `ec2:DescribeNetworkInterfaces` to `docs/production.md`.
- **Detection table row** (in root `CLAUDE.md`):
  ```
  | AmazonEC2 (SG) | ec2:DescribeSecurityGroups + DescribeNetworkInterfaces + cross-SG ref check | no ENI, no cross-ref, not default | Unused security group | $0 (hygiene) |
  ```
- **Test cases:** SG with ENI → not flagged; SG referenced by another SG → not flagged; default SG → never flagged; truly unused → flagged.

## Sequencing

| | Status | Scope | Approx PRs |
|---|---|---|---|
| **M1 — Refactor** | done | Split `discover.go` into per-service files | 1 |
| **M2 — Compute waste** | pending | P0 #1, #2, #3, #6 (SGs, target groups, overprovisioned RDS, tighter SageMaker) | 4 |
| **M3 — Data-store waste** | pending | P0 #4, #5; P1 #9, #10, #18 (ElastiCache RG, OpenSearch tightening, Redshift snaps, low-util DynamoDB, Aurora cluster) | 5 |
| **M4 — Network + automation hygiene** | pending | P1 #7, #8, #11, #12, #13; P2 #14, #15, #16 (VPC endpoints, TGW, Lambda PCU, NAT route, IAM keys, GA, Step Functions, CodeBuild) | 8 |
| **M5 — Long tail (stretch)** | pending | P2 #17, #19, #20 (cross-region snaps, KMS, EFS) | 3 |

Each PR < ~500 LOC including tests + docs. Milestones ship independently.

## Done criteria

The initiative is complete when:

1. All P0 + P1 rules (13 rules) are merged with unit tests + CLAUDE.md table updates.
2. No single file in `services/ingestion/internal/provider/aws/` exceeds ~400 lines.
3. The IAM policy in `docs/production.md` is updated and validated against a real test account.
4. A smoke scan against an AWS account produces ≥ 1 finding from at least 18 distinct rules.

## Success metrics

Tracked via `services/shared/observability/`:

- **Median detected savings per scan** (`axiaops_potential_monthly_savings_usd`): target +50% vs pre-initiative baseline.
- **Unique services covered:** 11 today → 18+ at completion.
- **Detection coverage by spend:** % of customer monthly AWS bill represented by analyzable services. Target > 85% of typical spend.
- **False-positive rate per rule:** dismissals / findings. Target < 15%. Any rule > 25% in production gets logic review before further rules ship.

## Provenance

Architectural decision and backlog produced by the project's `architect` and `Plan` agents (April 2026). The decision was: don't adopt Komiser, Cloud Custodian, OpenCost, Infracost, or CloudQuery as runtime dependencies — they're products with single-tenant or different-product-shape assumptions that don't fit a multi-tenant SaaS or the €24–34/mo cost target. Instead, mine Custodian's rule library as docs.
