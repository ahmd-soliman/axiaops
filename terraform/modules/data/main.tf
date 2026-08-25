resource "aws_db_subnet_group" "main" {
  name       = "axiaops-${var.env_name}"
  subnet_ids = var.public_subnet_ids

  tags = {
    Name = "axiaops-${var.env_name}"
  }
}

# The parameter group's NAME carries the family suffix on purpose:
# `aws_db_parameter_group.family` is immutable, so a major-version bump
# (postgres16 → postgres17) forces destroy-then-create. With a name-pinned
# resource the destroy deadlocks against the still-running RDS instance
# referencing it — same footgun the cache module's parameter group hit on
# 2026-05-27 (see modules/cache/main.tf for the long-form rationale).
# Encoding the family in the name + `lifecycle.create_before_destroy` lets
# Terraform stage the new group, swap the RDS reference, then destroy the
# old one cleanly. Subsequent major bumps (postgres17 → postgres18 etc.)
# repeat the pattern with no manual intervention.
resource "aws_db_parameter_group" "main" {
  name   = "axiaops-${var.env_name}-${var.parameter_group_family}"
  family = var.parameter_group_family

  parameter {
    name  = "rds.force_ssl"
    value = "1"
    # rds.force_ssl is a STATIC PostgreSQL parameter — changes require a DB
    # reboot to take effect. The provider defaults apply_method to "immediate",
    # but AWS silently coerces that to "pending-reboot" because the parameter
    # is static, producing a perpetual no-op diff on every plan. Pinning the
    # method to "pending-reboot" matches what AWS actually stores and kills the
    # drift. No reboot is queued because the value is unchanged.
    apply_method = "pending-reboot"
  }

  tags = {
    Name = "axiaops-${var.env_name}-${var.parameter_group_family}"
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_db_instance" "main" {
  identifier = "axiaops-${var.env_name}"

  engine         = "postgres"
  engine_version = var.engine_version
  instance_class = var.instance_class

  allocated_storage = var.allocated_storage_gb
  storage_type      = "gp3"
  storage_encrypted = true

  db_name  = var.db_name
  username = var.master_username
  # `password` and `manage_master_user_password` are ConflictsWith in the AWS
  # provider — setting both (even with `manage_master_user_password = false`)
  # fails plan. We manage the password ourselves via `var.owner_password`
  # (sourced from SSM), so omit `manage_master_user_password` entirely; the
  # default (Secrets Manager NOT used) is what we want.
  password = var.owner_password
  port     = 5432

  vpc_security_group_ids = [var.rds_sg_id]
  db_subnet_group_name   = aws_db_subnet_group.main.name
  parameter_group_name   = aws_db_parameter_group.main.name

  # publicly_accessible=false is the primary blast-radius limit (§5.2). The
  # SG is defence-in-depth; if both layers drift to permissive, AWS still
  # refuses to assign a public IP, so internet ingress can't route here.
  publicly_accessible = false
  multi_az            = false

  backup_retention_period = var.backup_retention_days
  backup_window           = "02:00-03:00"
  maintenance_window      = "sun:03:30-sun:04:30"

  deletion_protection       = var.deletion_protection
  skip_final_snapshot       = var.skip_final_snapshot
  final_snapshot_identifier = "axiaops-${var.env_name}-final-snapshot"
  copy_tags_to_snapshot     = true

  performance_insights_enabled = false
  apply_immediately            = var.apply_immediately

  # Major-version upgrades (e.g. postgres16 → postgres17) require this flag
  # to be true at plan time; otherwise the provider rejects an
  # engine_version change that crosses majors. Default false on the module
  # so callers must opt in (e.g. during an upgrade window) and revert
  # afterwards — flipping back to false is the safety latch against an
  # accidental future major bump from a routine engine_version edit.
  allow_major_version_upgrade = var.allow_major_version_upgrade

  lifecycle {
    # final_snapshot_identifier is consulted only at destroy, which we never
    # do via TF (deletion_protection = true). Static value + ignore_changes
    # keeps `terraform plan` clean (§6.1).
    ignore_changes = [final_snapshot_identifier]
  }

  tags = {
    Name = "axiaops-${var.env_name}"
  }
}
