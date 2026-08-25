variable "env_name" {
  description = "Environment name used as a resource-identifier prefix."
  type        = string
}

variable "vpc_id" {
  description = "VPC ID — used to scope the cache security group."
  type        = string
}

variable "subnet_ids" {
  description = "Subnet IDs (one per AZ) for the ElastiCache subnet group. Mirrors the RDS subnet-group shape — same VPC, no public addressability."
  type        = list(string)
}

variable "runtime_sg_id" {
  description = "ECS runtime security group (Express services + migrate task) — granted ingress on 6379, and an egress-to-cache rule is attached to it here (this module owns both SGs, so the rule lives here to avoid a network<->cache cycle)."
  type        = string
}

variable "migrate_sg_id" {
  description = "Optional separate ECS migrate task security group — granted ingress on 6379. Empty when the migrate task shares the runtime SG (the default today)."
  type        = string
  default     = ""
}

variable "engine_version" {
  description = "ElastiCache Valkey engine version. eu-central-1 offers 7.2 / 8.0 / 8.1 / 8.2 / 9.0. Default `8.2` (family valkey8). TWO-HOP path: the redis 7.x → valkey transition lands on valkey 7.2 first (the drop-in fork of redis 7.2.4); only from there is valkey 7.2 → 8.2 a valid valkey→valkey upgrade. Do NOT apply this against a cluster still pending the redis→valkey-7.2 swap — let that land first (docs/elasticache-engine-upgrade.md)."
  type        = string
  default     = "8.2"
}

variable "node_type" {
  description = "ElastiCache node type. Default cache.t4g.micro mirrors the cost-conscious db.t4g.micro shape of the RDS instance."
  type        = string
  default     = "cache.t4g.micro"
}

variable "parameter_group_family" {
  description = "Parameter-group family. Must align with engine_version's major: `valkey7` for 7.2, `valkey8` for 8.x, `valkey9` for 9.x. References AWS-managed `default.$${family}` (no custom group — see main.tf)."
  type        = string
  default     = "valkey8"
}

variable "snapshot_retention_limit" {
  description = "Daily snapshots retained. 0 disables snapshots — fine for a cache that has no source-of-truth data."
  type        = number
  default     = 0
}
