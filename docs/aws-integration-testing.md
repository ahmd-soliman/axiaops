# AWS Integration Testing — Real AWS, Not Emulators

How AxiaOps tests the AWS-touching layer of the ingestion service, and **why we deliberately do not depend on local AWS emulators for product correctness**.

> **Status:** Decision recorded 2026-05-09. Companion to `docs/TEST_STRATEGY.md` (which covers the unit/integration tier split). This doc is the AWS-specific cut.

---

## 1. The decision

For any test path that exercises code in `services/ingestion/internal/provider/aws/` or that asserts end-to-end zombie-detection correctness, AxiaOps tests against **real AWS APIs running against a small, dedicated AWS account with stable test resources** — not against a local AWS emulator.

Local emulators (Floci, kumo, mummer) may continue to exist as offline-development conveniences (e.g. for `make start-dev` when the laptop is offline), but they are **not load-bearing for QA**. No release decision rides on emulator-based tests passing.

This is a deliberate departure from the LocalStack-replacement-replacement direction the conversation in early May 2026 was drifting toward.

---

## 2. Why not emulators

For most products, "wire-compatible local AWS emulator" is a reasonable test target. For a FinOps product whose entire value proposition is "we correctly identify the waste in your AWS account," it is not.

The failure modes specific to AxiaOps:

- **Subtle wire divergence is the cost-of-being-wrong.** An emulator that returns `StateTransitionReason` in a slightly different format than real AWS, or pages `DescribeNatGateways` with different `NextToken` semantics, or omits a field that AxiaOps's analyzer reads for the threshold check, produces tests that pass and code that ships subtly-wrong detections. "We told you you had a zombie that wasn't" or "we missed €40k of waste because the emulator returned `LastAccessedDate` as epoch seconds and real AWS returns ISO8601" is the category of bug that destroys FinOps-tool trust faster than any other.
- **No emulator models cross-resource linkage.** Detection rules join `EC2.DescribeInstances` results to `CloudWatch.GetMetricStatistics` results (via `Dimensions=[InstanceId=…]`) to `CostExplorer.GetCostAndUsageWithResources` results (via `resource_id=…`). All three emulators store these as disjoint islands — the operator types `i-abc123` into three separate fixture files and has to keep them in sync by eye. A typo in any one fixture causes `analyzer.Detect()` to return zero zombies silently — the test passes a tautology. See §5 for the full structural analysis.
- **Wire-compat depth varies and is unauditable at scale.** Floci ships ~1,850 SDK compatibility tests across six SDKs and three IaC tools — the strongest available signal. Kumo claims SDK-v2 compat with one integration test per service. Mummer is pre-alpha. None of these gives the same trust level as actually calling AWS.
- **Emulators encode our model of AWS, not AWS.** Real AWS adds a field, deprecates an endpoint, changes a pagination behavior, or fixes a quirk. Emulator-based tests pass; production breaks. This is a treadmill we choose not to walk.

For the specific risk profile of a FinOps tool shipping to paying customers, the right amount of emulator dependency is **zero** for QA-load-bearing paths.

---

## 3. What we evaluated and why we passed

Three Go/Java AWS emulators were measured against AxiaOps's actual AWS surface (the 25 distinct API calls in `services/ingestion/internal/provider/aws/discover_*.go` + `ceapi.go` + `cwapi.go`).

### Coverage of AxiaOps's call set

| AxiaOps API call | Floci (upstream) | mummer | kumo |
|---|:---:|:---:|:---:|
| CostExplorer.GetCostAndUsage | absent | present + fixture | present + fixture (locally added) |
| CloudWatch.GetMetricStatistics | present | present + fixture | present |
| EC2.DescribeInstances / Volumes / Images / Addresses | present | absent | partial (Instances only) |
| EC2.DescribeSnapshots | absent | absent | absent |
| EC2.DescribeNatGateways | absent | absent | present |
| RDS.DescribeDBInstances | present | absent | present |
| RDS.DescribeDBSnapshots | absent | absent | absent |
| Lambda.ListFunctions | present | present | present |
| ELBv2.DescribeLoadBalancers | present | absent | present |
| CloudFront.ListDistributions | absent | absent | present |
| Kinesis.ListStreams | present | absent | present |
| S3 / DynamoDB / STS / CW Logs | present | present | present |
| SecretsManager.ListSecrets | present | absent | present |
| ECR / ElastiCache | present | absent | present |
| OpenSearch.ListDomainNames | present | absent | absent |
| Redshift.DescribeClusters | absent | absent | present |
| SageMaker.ListEndpoints | absent | absent | absent (only DescribeEndpoint) |
| EKS.ListClusters | present | absent | present |
| **Total of 25** | **16/25** | **6/25** | **18/25** |

### Depth where they overlap

Where a service exists in more than one emulator, we measured by lines-of-code and by count of dispatched operations:

- **Floci is roughly 2× deeper on operations per service** than kumo. EC2: 65 ops vs 28. ELBv2: 34 vs 11. Kinesis: 27 vs 10.
- **Mummer is densest per service where it bothers** (DynamoDB 6,200 LOC / 30 ops; Lambda 4,874 / 42; CE 4,167 LOC + 2,257 LOC of tests + a real Expression-evaluator with And/Or/Not validation), but covers only ~10 services.
- **Kumo is broadest** (76 services, ~1,300 LOC × 10 ops average), shallowest per service.

### Why each was rejected

- **Floci** — broad coverage and the strongest compat-test discipline of the three, but Java/Quarkus stack is friction for our Go shop and **no Cost Explorer service at all**, which would need to be implemented from scratch in a stack the team doesn't otherwise touch. ~6 detection rules unreachable without further upstream contributions (orphaned snapshots × 2, unused NAT GW, CloudFront, Redshift, SageMaker endpoints).
- **kumo** — the strongest single emulator for AxiaOps in raw coverage (18/25 with the Cost Explorer fixture we contributed). Go-native, single static binary, would be a clean fit. Rejected for the structural-trust reason in §2, not coverage.
- **mummer** — own project; deepest CE filter-expression semantics; 6/25 coverage and pre-alpha. Decoupled from AxiaOps's QA story going forward; continues as a personal OSS project on its own merits.

None of the three closed the cross-resource linkage gap (§5), and that is what made the decision unconditional rather than coverage-based.

---

## 4. What we do instead

### 4.1 Layered test strategy (AWS-touching code only)

Three layers, each with a different trust level and a different cost:

| Layer | Trust source | Speed | Cost | What it certifies |
|---|---|---|---|---|
| **Unit / handler** — `go test ./...` | Mocks (`mockCEClient`, `mockCWClient`) + captured-from-real-AWS golden response fixtures | <1s | free | Our code parses real-shaped responses correctly and constructs well-formed requests |
| **Integration** — `make test-integration` (no AWS calls) | Docker-compose stack with mocked AWS provider, real PostgreSQL/Redis | ~30s | free | Our ingest → analyze → store → API pipeline works end-to-end against the same mocked wire shapes |
| **Acceptance / canary** — staging deployment polling a real AWS test fleet on a schedule | Real AWS APIs, real responses, real attribute shapes | minutes | ~€20–40/mo | Our code talks to *actual* AWS without divergence; catches "AWS quietly changed X" before customers do |

The unit layer is where 95% of the testing volume lives. The acceptance layer is the irreducible truth source for wire compatibility.

### 4.2 The real AWS test fleet

A single dedicated AWS account, provisioned via Terraform from a developer laptop (never CI), holding a stable set of resources designed to exercise every detection rule:

| Resource | Purpose | Approximate monthly cost |
|---|---|---|
| 1× t4g.nano EC2, idle | CPU ≤ 5% rule | ~€3 |
| 1× db.t4g.micro RDS, zero connections | DatabaseConnections = 0 rule | ~€12 |
| 1× small Lambda, never invoked | Invocations = 0 rule | €0 |
| 1× unattached EIP | NetworkInterfaceAttachment = 0 rule | ~€3 |
| 1× unattached EBS volume | state = "available" rule | ~€1 |
| 1× orphaned EBS snapshot | source-volume-gone rule | ~€0.50 |
| 1× untagged ECR image | age > 90 days rule | ~€0.10 |
| 1× CloudWatch log group, retention=null | retention-not-set rule | ~€0.10 |
| 1× RDS snapshot (manual, source DB deleted) | orphaned RDS snapshot rule | ~€0.50 |
| 1× idle ELB (zero requests) | RequestCount = 0 rule | ~€18 |
| **Total** | | **~€38/mo** |

The fleet is itself an AxiaOps zombie target — eating our own dogfood. Expected to be detected and reported by every staging scan.

### 4.3 Cost containment

AWS does not provide a hard spending kill-switch; soft caps via:

- **AWS Budgets** at €60/mo (≈1.5× expected) with email alert at 80%, **Budgets Action** at 100% that stops EC2/RDS instances and detaches the integration-test IAM policy. Stops further compute spend within minutes of crossing the threshold.
- **Service Quotas** capping max EC2 instances at 5, RDS at 2 — limits blast radius if a bug or credential leak triggers `RunInstances` storms.
- **SCP** (if the test account is part of an Organization) denying expensive instance families (p*, x*, large EC2 sizes).

### 4.4 IAM scope — read-only by construction

The IAM role used by the staging canary has **no `*:Create*` / `*:Run*` / `*:Modify*` / `*:Delete*` permissions whatsoever**. The role can call:

- `ec2:Describe*`
- `rds:Describe*`
- `lambda:List*` / `lambda:Get*`
- `elasticloadbalancing:Describe*`
- `cloudfront:List*` / `cloudfront:Get*`
- `kinesis:List*` / `kinesis:Describe*`
- `s3:List*` / `s3:GetBucketLocation`
- `logs:Describe*`
- `secretsmanager:List*` / `secretsmanager:Describe*`
- `ecr:Describe*`
- `elasticache:Describe*`
- `es:List*` / `es:Describe*`
- `redshift:Describe*`
- `sagemaker:List*` / `sagemaker:Describe*`
- `dynamodb:List*` / `dynamodb:Describe*`
- `eks:List*` / `eks:Describe*`
- `cloudwatch:Get*` / `cloudwatch:List*`
- `ce:GetCostAndUsage` / `ce:GetCostAndUsageWithResources`
- `sts:GetCallerIdentity`

Worst-case credential leak: a few thousand extra Describe calls (free) — not a p4d.24xlarge spinning up over a weekend.

### 4.5 Captured fixtures from real AWS

A one-time `tools/capture-aws-fixtures` script (to be built) calls every Describe / List / Get-Cost-And-Usage endpoint once against the test fleet and snapshots the JSON responses into `services/ingestion/internal/provider/aws/testdata/`. From then on, unit tests replay the snapshots deterministically. Re-capture on a schedule (or manually after AWS signals a change) to keep fixtures fresh.

This gives wire-compat truthfulness in unit tests *and* deterministic CI without paying per run, while avoiding emulator-maintenance treadmill.

### 4.6 Staging canary loop

Staging runs the actual ingestion service against the test fleet on a schedule (hourly / daily — TBD). The output is monitored for drift before each release: zombie counts, summary totals, per-service breakdown. A delta against the previous staging run's baseline triggers an alert. The canary is the single mechanism that catches "AWS changed `DescribeNatGateways` pagination semantics" — no emulator, captured fixture, or unit test will surface that.

---

## 5. The cross-resource linkage gap (why this also rules out the emulator path long-term)

Every detection rule in `analyzer/detector.go` joins three independent AWS API responses on a common resource ID:

```
EC2.DescribeInstances           → instance i-abc123 with tags
CW.GetMetricStatistics          → namespace=AWS/EC2 metric=CPUUtilization
                                  Dimensions=[InstanceId=i-abc123] avg=2%
CE.GetCostAndUsageWithResources → row resource_id=i-abc123 amount=$50/mo
```

In all three emulators evaluated, these three stores are disjoint:

- **Mummer** — CW fixture is keyed on `(namespace, metric_name, dimensions{string→string})`. Dimension values are free-text strings the operator types in. No validator checks whether `i-abc123` exists in any other service's storage. `mummer-seed`'s `Scenario` struct covers S3/DynamoDB/Lambda/SQS/SNS/Logs/IAM and has no cross-service-ID concept.
- **kumo** — same shape: `MetricDatum.Dimensions []Dimension` with `(Name, Value)` strings. The CE `FixtureRow` carries `LinkedAccount / Service / Region / Tags / Metrics` but no `ResourceID` field, even though AxiaOps's `ceapi.go` calls `GetCostAndUsageWithResources` (the resource-level CE variant where the resource ID is the whole point).
- **Floci** — worst on this axis: no fixture-seeding pattern for CloudWatch at all. To get metric data in, one calls `PutMetricData` against the running emulator. Linkage is whatever the operator constructs in their seed script.

Result: even if you adopt one of these emulators, the operator must manually keep `i-abc123` consistent across three separate fixture files. A typo causes silent test pass with `0 zombies` reported — the integration test certifies that the analyzer can read empty input, not that it detects anything. Closing this gap requires a higher layer above any of the emulators (a single scenario file → derived per-service fixtures, with cross-references enforced at scenario-load time). That layer does not exist, would be AxiaOps-specific code to build, and **even when complete still tests against a model of AWS rather than AWS itself**. Not worth building given the §2 reasoning.

---

## 7. Operational follow-ups

These are the concrete tasks this decision unblocks. None are in flight yet — track as follow-up items:

- Provision the test fleet via Terraform (~½ day; lives in a separate repo or `infra/` subdirectory, not in the main app repo).
- Build `tools/capture-aws-fixtures` to snapshot real AWS responses into `testdata/` (~1 day).
- Wire the staging canary to poll the test fleet on a schedule (~½ day on top of existing scheduled-scan plumbing).
- AWS Budgets + Budgets Actions setup (~½ hour, manual / Terraform).
- Read-only IAM role + role-assumption flow for the staging canary (~½ day).
- Document the test-fleet recovery procedure: how to re-create the fleet from Terraform if the account is lost (~½ day).

Total ~3 days of focused work to land the real-AWS test foundation, after which we never re-evaluate the emulator question.
