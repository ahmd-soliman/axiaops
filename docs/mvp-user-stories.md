Here are the refined AxiaOps Phase 1 tickets, rewritten into professional User Story format (Role + Action + Value). I have integrated the "Cloud Hygiene" and "Metric-Aware" improvements into these stories.
📋 AxiaOps Phase 1: User Story Backlog

AXIAOPS-001 | Synthetic Billing & Metrics Generator

User Story:

    As a Developer,

    I want to generate a synchronized dataset of billing CSVs and CloudWatch-style JSON metrics,

    So that I can build detection logic that differentiates between "truly idle" resources and "scheduled batch" resources without using real customer data.

Acceptance Criteria:

    [ ] Script generates 10,000 billing rows cross-referenced with usage metrics (CPU/Network).

    [ ] Includes random Owner and Project tags for every resource.

    [ ] Implements a --seed flag for deterministic testing.

    [ ] Output files are valid CSV and JSON, stored in a /test_data directory.

AXIAOPS-002 | Go Backend: Multi-Source Ingestion Engine

User Story:

    As a System Architect,

    I want a Go-based ingestion worker that joins billing data with usage metrics,

    So that the system has a holistic view of both cost and activity for every cloud asset.

Acceptance Criteria:

    [ ] Worker uses Go routines to parse CSV and JSON concurrently.

    [ ] Data is persisted to a PostgreSQL database with an organization_id for future multi-tenancy.

    [ ] Successfully maps line_item_resource_id to its corresponding activity metrics.

    [ ] Handles malformed input without crashing the entire ingestion process.

AXIAOPS-003 | Go Backend: Smart Detection & Ownership Logic

User Story:

    As a FinOps Analyst,

    I want the engine to flag resources as "Ghosts" only when both cost is present and activity is below a defined threshold,

    So that I don't annoy engineering teams with "false positive" deletion requests.

Acceptance Criteria:

    [ ] Logic identifies EBS volumes with 0 IOPS and Elastic IPs with no associations.

    [ ] Every flagged "Ghost" includes an attributed Owner based on resource tags.

    [ ] Calculates potential_monthly_savings based on a 30-day projection of the daily leak.

    [ ] Unit tests confirm that tagged "Production" resources require a higher threshold for flagging.

AXIAOPS-004 | API: Actionable Insights Endpoints

User Story:

    As a Frontend Developer,

    I want clear REST endpoints that provide categorized savings data and remediation commands,

    So that the mobile app can display immediate, actionable "fixes" to the user.

Acceptance Criteria:

    [ ] GET /summary returns total leakage, efficiency scores, and savings by Project.

    [ ] GET /ghosts returns a list of resources including a remediation_snippet (CLI/Terraform).

    [ ] POST /ignore allows the user to whitelist a specific resource ID in the database.

    [ ] All responses are returned as valid, minified JSON.

AXIAOPS-005 | Frontend: Mobile App Architecture

User Story:

    As a Mobile User,

    I want a lightweight, fast-loading application shell,

    So that I can check my cloud spend and "Ghost" count while on the go.

Acceptance Criteria:

    [ ] Expo project initialized with TypeScript and a bottom-tab navigation pattern.

    [ ] React Query is configured to handle API caching and background refreshing.

    [ ] App successfully connects to the AXIAOPS-004 API running on a local or staging IP.

    [ ] Global state manages the "Current Account" context (mocking multi-tenancy).

AXIAOPS-006 | Frontend: "Pocket CFO" Dashboard UI

User Story:

    As a CTO/CFO,

    I want to see a high-contrast dashboard showing my total "Monthly Leak" and a list of who is responsible,

    So that I can drive accountability and reduce cloud waste across the organization.

Acceptance Criteria:

    [ ] Hero section displays "Potential Monthly Savings" in high-visibility Red/Green.

    [ ] "Ghost List" is filterable by Owner and Resource Type.

    [ ] Tapping a resource opens a modal showing the "Fix it" command (CLI or IaC).

    [ ] UI supports "Pull-to-Refresh" to trigger a new scan from the