package catalog

import (
	"sort"
	"strings"
)

// ServiceSpec is the single source of truth for service metadata used by the
// scanner and TUI navigation.
type ServiceSpec struct {
	ID          string
	DisplayName string

	DefaultType   string
	FallbackTypes []string

	SampleType  string
	SampleLabel string
}

var serviceSpecs = map[string]ServiceSpec{
	"accessanalyzer": {
		ID:            "accessanalyzer",
		DisplayName:   "Access Analyzer",
		DefaultType:   "accessanalyzer:analyzer",
		FallbackTypes: []string{"accessanalyzer:analyzer"},
		SampleType:    "accessanalyzer:analyzer",
		SampleLabel:   "analyzers",
	},
	"acm": {
		ID:            "acm",
		DisplayName:   "ACM",
		DefaultType:   "acm:certificate",
		FallbackTypes: []string{"acm:certificate"},
		SampleType:    "acm:certificate",
		SampleLabel:   "certificates",
	},
	"apigateway": {
		ID:            "apigateway",
		DisplayName:   "API Gateway",
		DefaultType:   "apigateway:rest-api",
		FallbackTypes: []string{"apigateway:rest-api", "apigateway:domain-name"},
		SampleType:    "apigateway:rest-api",
		SampleLabel:   "apis",
	},
	"autoscaling": {
		ID:            "autoscaling",
		DisplayName:   "Auto Scaling",
		DefaultType:   "autoscaling:group",
		FallbackTypes: []string{"autoscaling:group", "autoscaling:launch-configuration", "autoscaling:instance"},
		SampleType:    "autoscaling:group",
		SampleLabel:   "groups",
	},
	"cloudfront": {
		ID:            "cloudfront",
		DisplayName:   "CloudFront",
		DefaultType:   "cloudfront:distribution",
		FallbackTypes: []string{"cloudfront:distribution"},
		SampleType:    "cloudfront:distribution",
		SampleLabel:   "distributions",
	},
	"cloudtrail": {
		ID:            "cloudtrail",
		DisplayName:   "CloudTrail",
		DefaultType:   "cloudtrail:trail",
		FallbackTypes: []string{"cloudtrail:trail"},
		SampleType:    "cloudtrail:trail",
		SampleLabel:   "trails",
	},
	"config": {
		ID:            "config",
		DisplayName:   "Config",
		DefaultType:   "config:recorder",
		FallbackTypes: []string{"config:recorder", "config:delivery-channel"},
		SampleType:    "config:recorder",
		SampleLabel:   "recorders",
	},
	"dynamodb": {
		ID:            "dynamodb",
		DisplayName:   "DynamoDB",
		DefaultType:   "dynamodb:table",
		FallbackTypes: []string{"dynamodb:table"},
		SampleType:    "dynamodb:table",
		SampleLabel:   "tables",
	},
	"ec2": {
		ID:          "ec2",
		DisplayName: "EC2",
		DefaultType: "ec2:instance",
		FallbackTypes: []string{
			"ec2:instance", "ec2:volume", "ec2:snapshot", "ec2:ami", "ec2:nat-gateway", "ec2:eip",
			"ec2:network-interface", "ec2:route-table", "ec2:nacl", "ec2:internet-gateway",
			"ec2:launch-template", "ec2:key-pair", "ec2:placement-group", "ec2:security-group",
			"ec2:subnet", "ec2:vpc",
		},
		SampleType:  "ec2:instance",
		SampleLabel: "instances",
	},
	"ecr": {
		ID:            "ecr",
		DisplayName:   "ECR",
		DefaultType:   "ecr:repository",
		FallbackTypes: []string{"ecr:repository"},
		SampleType:    "ecr:repository",
		SampleLabel:   "repositories",
	},
	"ecs": {
		ID:            "ecs",
		DisplayName:   "ECS",
		DefaultType:   "ecs:cluster",
		FallbackTypes: []string{"ecs:cluster", "ecs:service", "ecs:task", "ecs:task-definition"},
		SampleType:    "ecs:cluster",
		SampleLabel:   "clusters",
	},
	"efs": {
		ID:            "efs",
		DisplayName:   "EFS",
		DefaultType:   "efs:file-system",
		FallbackTypes: []string{"efs:file-system", "efs:mount-target", "efs:access-point"},
		SampleType:    "efs:file-system",
		SampleLabel:   "filesystems",
	},
	"eks": {
		ID:            "eks",
		DisplayName:   "EKS",
		DefaultType:   "eks:cluster",
		FallbackTypes: []string{"eks:cluster", "eks:nodegroup"},
		SampleType:    "eks:cluster",
		SampleLabel:   "clusters",
	},
	"elasticache": {
		ID:            "elasticache",
		DisplayName:   "ElastiCache",
		DefaultType:   "elasticache:replication-group",
		FallbackTypes: []string{"elasticache:replication-group", "elasticache:cache-cluster", "elasticache:subnet-group"},
		SampleType:    "elasticache:replication-group",
		SampleLabel:   "replication groups",
	},
	"elbv2": {
		ID:            "elbv2",
		DisplayName:   "ELBv2",
		DefaultType:   "elbv2:load-balancer",
		FallbackTypes: []string{"elbv2:load-balancer", "elbv2:target-group", "elbv2:listener", "elbv2:rule"},
		SampleType:    "elbv2:load-balancer",
		SampleLabel:   "load balancers",
	},
	"guardduty": {
		ID:            "guardduty",
		DisplayName:   "GuardDuty",
		DefaultType:   "guardduty:detector",
		FallbackTypes: []string{"guardduty:detector"},
		SampleType:    "guardduty:detector",
		SampleLabel:   "detectors",
	},
	"iam": {
		ID:            "iam",
		DisplayName:   "IAM",
		DefaultType:   "iam:role",
		FallbackTypes: []string{"iam:user", "iam:group", "iam:access-key", "iam:role", "iam:policy"},
		SampleType:    "iam:role",
		SampleLabel:   "roles",
	},
	"identitycenter": {
		ID:            "identitycenter",
		DisplayName:   "Identity Center",
		DefaultType:   "identitycenter:permission-set",
		FallbackTypes: []string{"identitycenter:permission-set", "identitycenter:assignment", "identitycenter:user", "identitycenter:group", "identitycenter:instance"},
		SampleType:    "identitycenter:permission-set",
		SampleLabel:   "permission sets",
	},
	"kms": {
		ID:            "kms",
		DisplayName:   "KMS",
		DefaultType:   "kms:key",
		FallbackTypes: []string{"kms:key", "kms:alias"},
		SampleType:    "kms:key",
		SampleLabel:   "keys",
	},
	"lambda": {
		ID:            "lambda",
		DisplayName:   "Lambda",
		DefaultType:   "lambda:function",
		FallbackTypes: []string{"lambda:function"},
		SampleType:    "lambda:function",
		SampleLabel:   "functions",
	},
	"logs": {
		ID:            "logs",
		DisplayName:   "CloudWatch Logs",
		DefaultType:   "logs:log-group",
		FallbackTypes: []string{"logs:log-group"},
	},
	"msk": {
		ID:            "msk",
		DisplayName:   "MSK",
		DefaultType:   "msk:cluster",
		FallbackTypes: []string{"msk:cluster"},
		SampleType:    "msk:cluster",
		SampleLabel:   "clusters",
	},
	"opensearch": {
		ID:            "opensearch",
		DisplayName:   "OpenSearch",
		DefaultType:   "opensearch:domain",
		FallbackTypes: []string{"opensearch:domain"},
		SampleType:    "opensearch:domain",
		SampleLabel:   "domains",
	},
	"rds": {
		ID:            "rds",
		DisplayName:   "RDS",
		DefaultType:   "rds:db-instance",
		FallbackTypes: []string{"rds:db-instance", "rds:db-cluster", "rds:db-subnet-group"},
		SampleType:    "rds:db-instance",
		SampleLabel:   "dbs",
	},
	"redshift": {
		ID:            "redshift",
		DisplayName:   "Redshift",
		DefaultType:   "redshift:cluster",
		FallbackTypes: []string{"redshift:cluster", "redshift:subnet-group"},
		SampleType:    "redshift:cluster",
		SampleLabel:   "clusters",
	},
	"s3": {
		ID:            "s3",
		DisplayName:   "S3",
		DefaultType:   "s3:bucket",
		FallbackTypes: []string{"s3:bucket"},
		SampleType:    "s3:bucket",
		SampleLabel:   "buckets",
	},
	"sagemaker": {
		ID:            "sagemaker",
		DisplayName:   "SageMaker",
		DefaultType:   "sagemaker:endpoint",
		FallbackTypes: []string{"sagemaker:endpoint", "sagemaker:endpoint-config", "sagemaker:model", "sagemaker:notebook-instance", "sagemaker:training-job", "sagemaker:processing-job", "sagemaker:transform-job", "sagemaker:domain", "sagemaker:user-profile"},
		SampleType:    "sagemaker:endpoint",
		SampleLabel:   "endpoints",
	},
	"secretsmanager": {
		ID:            "secretsmanager",
		DisplayName:   "Secrets Manager",
		DefaultType:   "secretsmanager:secret",
		FallbackTypes: []string{"secretsmanager:secret"},
		SampleType:    "secretsmanager:secret",
		SampleLabel:   "secrets",
	},
	"securityhub": {
		ID:            "securityhub",
		DisplayName:   "Security Hub",
		DefaultType:   "securityhub:hub",
		FallbackTypes: []string{"securityhub:hub", "securityhub:standard-subscription"},
		SampleType:    "securityhub:hub",
		SampleLabel:   "hubs",
	},
	"sns": {
		ID:            "sns",
		DisplayName:   "SNS",
		DefaultType:   "sns:topic",
		FallbackTypes: []string{"sns:topic", "sns:subscription"},
		SampleType:    "sns:topic",
		SampleLabel:   "topics",
	},
	"sqs": {
		ID:            "sqs",
		DisplayName:   "SQS",
		DefaultType:   "sqs:queue",
		FallbackTypes: []string{"sqs:queue"},
		SampleType:    "sqs:queue",
		SampleLabel:   "queues",
	},
	"wafv2": {
		ID:            "wafv2",
		DisplayName:   "WAFv2",
		DefaultType:   "wafv2:web-acl",
		FallbackTypes: []string{"wafv2:web-acl"},
		SampleType:    "wafv2:web-acl",
		SampleLabel:   "web acls",
	},
}

func normalizeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// Lookup returns service metadata for an ID.
func Lookup(id string) (ServiceSpec, bool) {
	id = normalizeID(id)
	s, ok := serviceSpecs[id]
	if !ok {
		return ServiceSpec{}, false
	}
	s.FallbackTypes = cloneStringSlice(s.FallbackTypes)
	return s, true
}

// ListIDs returns all known service IDs sorted lexicographically.
func ListIDs() []string {
	ids := make([]string, 0, len(serviceSpecs))
	for id := range serviceSpecs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// All returns all service specs sorted by ID.
func All() []ServiceSpec {
	ids := ListIDs()
	out := make([]ServiceSpec, 0, len(ids))
	for _, id := range ids {
		s, ok := Lookup(id)
		if ok {
			out = append(out, s)
		}
	}
	return out
}

func DefaultType(id string) string {
	s, ok := Lookup(id)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s.DefaultType)
}

func FallbackTypes(id string) []string {
	s, ok := Lookup(id)
	if !ok {
		return nil
	}
	return cloneStringSlice(s.FallbackTypes)
}

func Sample(id string) (typ, label string) {
	s, ok := Lookup(id)
	if !ok {
		return "", ""
	}
	return strings.TrimSpace(s.SampleType), strings.TrimSpace(s.SampleLabel)
}
