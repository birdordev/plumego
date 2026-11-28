package migrations

import _ "embed"

//go:embed sql/bucket_protection.sql
var bucketProtectionSQL string

func BucketProtection() string { return bucketProtectionSQL }

//go:embed sql/bucket_protection_alerts.sql
var bucketProtectionAlertsSQL string

func BucketProtectionAlerts() string { return bucketProtectionAlertsSQL }

//go:embed sql/database_protection.sql
var databaseProtectionSQL string

func DatabaseProtection() string { return databaseProtectionSQL }

//go:embed sql/database_protection_posture.sql
var databaseProtectionPostureSQL string

func DatabaseProtectionPosture() string { return databaseProtectionPostureSQL }

//go:embed sql/database_protection_alerts.sql
var databaseProtectionAlertsSQL string

func DatabaseProtectionAlerts() string { return databaseProtectionAlertsSQL }

//go:embed sql/evaluate.sql
var evaluateSQL string

func Evaluate() string { return evaluateSQL }

//go:embed sql/freshness.sql
var freshnessSQL string

func Freshness() string { return freshnessSQL }

//go:embed sql/idle_report.sql
var idleReportSQL string

func IdleReport() string { return idleReportSQL }

//go:embed sql/project_budget_alerts.sql
var projectBudgetAlertsSQL string

func ProjectBudgetAlerts() string { return projectBudgetAlertsSQL }

//go:embed sql/project_summary.sql
var projectSummarySQL string

func ProjectSummary() string { return projectSummarySQL }

//go:embed sql/reconcile.sql
var reconcileSQL string

func Reconcile() string { return reconcileSQL }

//go:embed sql/reconcile_coverage.sql
var reconcileCoverageSQL string

func ReconcileCoverage() string { return reconcileCoverageSQL }

//go:embed sql/tag_compliance.sql
var tagComplianceSQL string

func TagCompliance() string { return tagComplianceSQL }

//go:embed sql/tag_policy_alerts.sql
var tagPolicyAlertsSQL string

func TagPolicyAlerts() string { return tagPolicyAlertsSQL }
