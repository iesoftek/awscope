package catalog

import "strings"

// ColumnKind describes how a resource-table column value is sourced.
type ColumnKind string

const (
	ColumnKindName    ColumnKind = "name"
	ColumnKindType    ColumnKind = "type"
	ColumnKindRegion  ColumnKind = "region"
	ColumnKindStatus  ColumnKind = "status"
	ColumnKindCreated ColumnKind = "created"
	ColumnKindID      ColumnKind = "id"
	ColumnKindAttr    ColumnKind = "attr"
	ColumnKindCost    ColumnKind = "cost"
)

// ValueFormat describes how a value should be rendered.
type ValueFormat string

const (
	ValueFormatText      ValueFormat = "text"
	ValueFormatStatus    ValueFormat = "status"
	ValueFormatBool      ValueFormat = "bool"
	ValueFormatInt       ValueFormat = "int"
	ValueFormatFloat     ValueFormat = "float"
	ValueFormatAgeDays   ValueFormat = "age_days"
	ValueFormatBytesGiB  ValueFormat = "bytes_gib"
	ValueFormatDateTime  ValueFormat = "datetime"
	ValueFormatListCount ValueFormat = "list_count"
)

// ColumnSpec defines one column in the resources table.
type ColumnSpec struct {
	ID       string
	Title    string
	AttrKey  string
	Kind     ColumnKind
	Format   ValueFormat
	Width    int
	MinWidth int
}

// TablePreset describes resource-table columns for a service/type.
type TablePreset struct {
	Service string
	Type    string
	Columns []ColumnSpec
}

func cloneColumns(in []ColumnSpec) []ColumnSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]ColumnSpec, len(in))
	copy(out, in)
	return out
}

func clonePreset(in TablePreset) TablePreset {
	in.Columns = cloneColumns(in.Columns)
	return in
}

func preset(service, typ string, cols ...ColumnSpec) TablePreset {
	return TablePreset{Service: normalizeID(service), Type: strings.ToLower(strings.TrimSpace(typ)), Columns: cloneColumns(cols)}
}

func colName() ColumnSpec {
	return ColumnSpec{ID: "name", Title: "Name", Kind: ColumnKindName, Format: ValueFormatText, Width: 24, MinWidth: 12}
}
func colType() ColumnSpec {
	return ColumnSpec{ID: "type", Title: "Type", Kind: ColumnKindType, Format: ValueFormatText, Width: 18, MinWidth: 10}
}
func colRegion() ColumnSpec {
	return ColumnSpec{ID: "region", Title: "Region", Kind: ColumnKindRegion, Format: ValueFormatText, Width: 11, MinWidth: 8}
}
func colStatus(attr string) ColumnSpec {
	return ColumnSpec{ID: "status", Title: "Status", AttrKey: strings.TrimSpace(attr), Kind: ColumnKindStatus, Format: ValueFormatStatus, Width: 12, MinWidth: 8}
}
func colCreated() ColumnSpec {
	return ColumnSpec{ID: "created", Title: "Created", AttrKey: "created_at", Kind: ColumnKindCreated, Format: ValueFormatDateTime, Width: 16, MinWidth: 10}
}
func colID() ColumnSpec {
	return ColumnSpec{ID: "id", Title: "ID", Kind: ColumnKindID, Format: ValueFormatText, Width: 22, MinWidth: 12}
}
func colAttr(id, title, key string, format ValueFormat, width, minWidth int) ColumnSpec {
	return ColumnSpec{ID: id, Title: title, AttrKey: key, Kind: ColumnKindAttr, Format: format, Width: width, MinWidth: minWidth}
}

var globalTablePreset = preset("", "",
	colName(),
	colType(),
	colRegion(),
	colStatus(""),
	colCreated(),
	colID(),
)

var serviceDefaults = map[string]TablePreset{
	"accessanalyzer": preset("accessanalyzer", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("analyzer_type", "AnalyzerType", "type", ValueFormatText, 12, 8),
		colAttr("last_analyzed", "LastAnalyzed", "lastResourceAnalyzedAt", ValueFormatDateTime, 16, 10),
		colCreated(), colID(),
	),
	"acm": preset("acm", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("domain", "Domain", "domain", ValueFormatText, 20, 10),
		colAttr("in_use", "InUse", "inUse", ValueFormatBool, 7, 5),
		colCreated(), colID(),
	),
	"apigateway": preset("apigateway", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("endpoint_mode", "EndpointMode", "endpointAccessMode", ValueFormatText, 12, 8),
		colCreated(), colID(),
	),
	"autoscaling": preset("autoscaling", "", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"cloudfront": preset("cloudfront", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("domain", "Domain", "domain", ValueFormatText, 24, 12),
		colAttr("enabled", "Enabled", "enabled", ValueFormatBool, 7, 5),
		colCreated(), colID(),
	),
	"cloudtrail": preset("cloudtrail", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("home_region", "HomeRegion", "homeRegion", ValueFormatText, 11, 8),
		colAttr("multi_region", "MultiRegion", "isMultiRegionTrail", ValueFormatBool, 11, 8),
		colCreated(), colID(),
	),
	"config": preset("config", "", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"dynamodb": preset("dynamodb", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("billing", "Billing", "billingMode", ValueFormatText, 9, 7),
		colAttr("sse", "SSE", "sseStatus", ValueFormatText, 8, 6),
		colCreated(), colID(),
	),
	"ecr": preset("ecr", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("mutability", "Mutability", "imageTagMutability", ValueFormatText, 12, 8),
		colAttr("encryption", "Encryption", "encryptionType", ValueFormatText, 11, 8),
		colCreated(), colID(),
	),
	"ec2": preset("ec2", "", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"ecs": preset("ecs", "", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"efs": preset("efs", "", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"eks": preset("eks", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("version", "Version", "version", ValueFormatText, 8, 6),
		colCreated(), colID(),
	),
	"elasticache": preset("elasticache", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("engine", "Engine", "engine", ValueFormatText, 10, 7),
		colAttr("node_type", "NodeType", "nodeType", ValueFormatText, 12, 8),
		colCreated(), colID(),
	),
	"elbv2": preset("elbv2", "", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"guardduty": preset("guardduty", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("features", "Features", "featuresCount", ValueFormatInt, 9, 6),
		colCreated(), colID(),
	),
	"iam":            preset("iam", "", colName(), colType(), colRegion(), colCreated(), colID()),
	"identitycenter": preset("identitycenter", "", colName(), colType(), colRegion(), colCreated(), colID()),
	"kms": preset("kms", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("manager", "Manager", "manager", ValueFormatText, 9, 7),
		colAttr("origin", "Origin", "origin", ValueFormatText, 8, 6),
		colCreated(), colID(),
	),
	"lambda": preset("lambda", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("runtime", "Runtime", "runtime", ValueFormatText, 12, 8),
		colAttr("memory", "Memory", "memorySize", ValueFormatInt, 8, 6),
		colCreated(), colID(),
	),
	"logs": preset("logs", "",
		colName(), colType(), colRegion(),
		colAttr("class", "Class", "class", ValueFormatText, 9, 6),
		colAttr("retention", "Retention", "retentionDays", ValueFormatInt, 10, 7),
		colAttr("stored", "Stored", "storedGiB|storedBytes", ValueFormatBytesGiB, 10, 8),
		colCreated(), colID(),
	),
	"msk": preset("msk", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("brokers", "Brokers", "brokerNodes", ValueFormatInt, 8, 6),
		colAttr("instance_type", "InstanceType", "instanceType", ValueFormatText, 12, 8),
		colCreated(), colID(),
	),
	"opensearch": preset("opensearch", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("engine", "Engine", "engine", ValueFormatText, 10, 7),
		colAttr("public", "Public", "publicDomain", ValueFormatBool, 7, 5),
		colCreated(), colID(),
	),
	"rds": preset("rds", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("engine", "Engine", "engine", ValueFormatText, 10, 7),
		colCreated(), colID(),
	),
	"redshift": preset("redshift", "",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("node_type", "NodeType", "nodeType", ValueFormatText, 12, 8),
		colAttr("public", "Public", "public", ValueFormatBool, 7, 5),
		colCreated(), colID(),
	),
	"s3": preset("s3", "",
		colName(), colType(), colRegion(),
		colAttr("encryption", "Encryption", "encryption", ValueFormatText, 11, 8),
		colAttr("public_block", "PublicBlock", "public_access_block", ValueFormatText, 12, 8),
		colCreated(), colID(),
	),
	"sagemaker":      preset("sagemaker", "", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"securityhub":    preset("securityhub", "", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"sns":            preset("sns", "", colName(), colType(), colRegion(), colCreated(), colID()),
	"sqs":            preset("sqs", "", colName(), colType(), colRegion(), colCreated(), colID()),
	"wafv2":          preset("wafv2", "", colName(), colType(), colRegion(), colStatus(""), colAttr("scope", "Scope", "scope", ValueFormatText, 8, 6), colCreated(), colID()),
	"secretsmanager": preset("secretsmanager", "", colName(), colType(), colRegion(), colAttr("rotation", "Rotation", "rotationEnabled", ValueFormatBool, 9, 6), colAttr("last_changed", "LastChanged", "lastChanged", ValueFormatDateTime, 16, 10), colCreated(), colID()),
}

var typeOverrides = map[string]TablePreset{
	"ec2:instance": preset("ec2", "ec2:instance",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("instance_type", "InstanceType", "instanceType", ValueFormatText, 12, 8),
		colAttr("az", "AZ", "az", ValueFormatText, 10, 7),
		colAttr("private_ip", "PrivateIP", "privateIp", ValueFormatText, 15, 10),
		colAttr("public_ip", "PublicIP", "publicIp", ValueFormatText, 15, 10),
		colCreated(), colID(),
	),
	"ec2:volume": preset("ec2", "ec2:volume",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("size_gib", "SizeGiB", "sizeGb", ValueFormatInt, 8, 6),
		colAttr("volume_type", "VolumeType", "volumeType", ValueFormatText, 11, 8),
		colAttr("encrypted", "Encrypted", "encrypted", ValueFormatBool, 9, 6),
		colAttr("az", "AZ", "az", ValueFormatText, 10, 7),
		colCreated(), colID(),
	),
	"ec2:snapshot": preset("ec2", "ec2:snapshot",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("volume_id", "VolumeId", "volumeId", ValueFormatText, 12, 8),
		colAttr("encrypted", "Encrypted", "encrypted", ValueFormatBool, 9, 6),
		colAttr("progress", "Progress", "progress", ValueFormatText, 9, 6),
		colCreated(), colID(),
	),
	"ec2:ami": preset("ec2", "ec2:ami",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("architecture", "Architecture", "architecture", ValueFormatText, 12, 8),
		colAttr("public", "Public", "isPublic", ValueFormatBool, 7, 5),
		colAttr("owner", "Owner", "imageOwnerAlias|owner", ValueFormatText, 12, 8),
		colCreated(), colID(),
	),
	"ec2:nat-gateway": preset("ec2", "ec2:nat-gateway",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("connectivity", "Connectivity", "connectivityType", ValueFormatText, 12, 8),
		colAttr("subnet", "Subnet", "subnetId", ValueFormatText, 12, 8),
		colAttr("vpc", "VPC", "vpcId", ValueFormatText, 12, 8),
		colCreated(), colID(),
	),
	"ec2:eip": preset("ec2", "ec2:eip",
		colName(), colType(), colRegion(),
		colAttr("public_ip", "PublicIP", "publicIp", ValueFormatText, 15, 10),
		colAttr("private_ip", "PrivateIP", "privateIp", ValueFormatText, 15, 10),
		colAttr("domain", "Domain", "domain", ValueFormatText, 9, 6),
		colAttr("instance_id", "InstanceId", "instanceId", ValueFormatText, 12, 8),
		colAttr("eni", "NetworkInterfaceId", "networkInterfaceId", ValueFormatText, 13, 9),
		colCreated(), colID(),
	),
	"ec2:network-interface": preset("ec2", "ec2:network-interface",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("interface_type", "InterfaceType", "interfaceType", ValueFormatText, 12, 8),
		colAttr("private_ip", "PrivateIP", "privateIp", ValueFormatText, 15, 10),
		colAttr("subnet", "Subnet", "subnet", ValueFormatText, 12, 8),
		colAttr("vpc", "VPC", "vpc", ValueFormatText, 12, 8),
		colCreated(), colID(),
	),
	"ec2:route-table": preset("ec2", "ec2:route-table",
		colName(), colType(), colRegion(),
		colAttr("vpc", "VPC", "vpc", ValueFormatText, 12, 8),
		colAttr("routes", "Routes", "routes", ValueFormatInt, 8, 6),
		colAttr("associations", "Associations", "associations", ValueFormatInt, 12, 8),
		colCreated(), colID(),
	),
	"ec2:nacl": preset("ec2", "ec2:nacl",
		colName(), colType(), colRegion(),
		colAttr("vpc", "VPC", "vpc", ValueFormatText, 12, 8),
		colAttr("is_default", "IsDefault", "isDefault", ValueFormatBool, 10, 7),
		colAttr("entries", "Entries", "entries", ValueFormatInt, 8, 6),
		colCreated(), colID(),
	),
	"ec2:internet-gateway": preset("ec2", "ec2:internet-gateway",
		colName(), colType(), colRegion(),
		colAttr("attachments", "Attachments", "attachments", ValueFormatInt, 11, 8),
		colCreated(), colID(),
	),
	"ec2:launch-template": preset("ec2", "ec2:launch-template",
		colName(), colType(), colRegion(),
		colAttr("latest_version", "LatestVer", "latestVersion", ValueFormatInt, 10, 7),
		colAttr("default_version", "DefaultVer", "defaultVersion", ValueFormatInt, 10, 7),
		colAttr("created_by", "CreatedBy", "createdBy", ValueFormatText, 15, 10),
		colCreated(), colID(),
	),
	"ec2:key-pair": preset("ec2", "ec2:key-pair",
		colName(), colType(), colRegion(),
		colAttr("key_type", "KeyType", "keyType", ValueFormatText, 9, 6),
		colAttr("fingerprint", "Fingerprint", "keyFingerprint", ValueFormatText, 14, 10),
		colCreated(), colID(),
	),
	"ec2:placement-group": preset("ec2", "ec2:placement-group",
		colName(), colType(), colRegion(), colStatus("state"),
		colAttr("strategy", "Strategy", "strategy", ValueFormatText, 10, 7),
		colAttr("partition_count", "PartitionCount", "partitionCount", ValueFormatInt, 14, 10),
		colCreated(), colID(),
	),
	"ec2:security-group": preset("ec2", "ec2:security-group",
		colName(), colType(), colRegion(),
		colAttr("vpc", "VPC", "vpc", ValueFormatText, 12, 8),
		colAttr("description", "Description", "description", ValueFormatText, 24, 10),
		colCreated(), colID(),
	),
	"ec2:subnet": preset("ec2", "ec2:subnet",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("az", "AZ", "az", ValueFormatText, 10, 7),
		colAttr("cidr", "CIDR", "cidr", ValueFormatText, 18, 10),
		colAttr("vpc", "VPC", "vpc", ValueFormatText, 12, 8),
		colCreated(), colID(),
	),
	"ec2:vpc": preset("ec2", "ec2:vpc",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("cidr", "CIDR", "cidr", ValueFormatText, 18, 10),
		colAttr("default_vpc", "Default", "isDefaultVpc", ValueFormatBool, 8, 6),
		colCreated(), colID(),
	),
	"autoscaling:group": preset("autoscaling", "autoscaling:group",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("desired", "Desired", "desiredCapacity", ValueFormatInt, 8, 6),
		colAttr("min", "Min", "minSize", ValueFormatInt, 6, 4),
		colAttr("max", "Max", "maxSize", ValueFormatInt, 6, 4),
		colAttr("instances", "Instances", "instances", ValueFormatInt, 9, 6),
		colCreated(), colID(),
	),
	"autoscaling:launch-configuration": preset("autoscaling", "autoscaling:launch-configuration",
		colName(), colType(), colRegion(),
		colAttr("instance_type", "InstanceType", "instanceType", ValueFormatText, 12, 8),
		colAttr("image_id", "ImageId", "imageId", ValueFormatText, 12, 8),
		colAttr("key_name", "KeyName", "keyName", ValueFormatText, 10, 7),
		colCreated(), colID(),
	),
	"autoscaling:instance": preset("autoscaling", "autoscaling:instance",
		colName(), colType(), colRegion(),
		colAttr("lifecycle", "Lifecycle", "lifecycleState", ValueFormatText, 11, 8),
		colAttr("health", "Health", "healthStatus", ValueFormatText, 8, 6),
		colAttr("az", "AZ", "availabilityZone", ValueFormatText, 10, 7),
		colCreated(), colID(),
	),
	"ecs:cluster": preset("ecs", "ecs:cluster",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("services", "Services", "activeServicesCount", ValueFormatInt, 8, 6),
		colAttr("running", "RunningTasks", "runningTasksCount", ValueFormatInt, 12, 8),
		colAttr("pending", "PendingTasks", "pendingTasksCount", ValueFormatInt, 12, 8),
		colCreated(), colID(),
	),
	"ecs:service": preset("ecs", "ecs:service",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("launch_type", "LaunchType", "launchType", ValueFormatText, 10, 7),
		colAttr("desired", "Desired", "desiredCount", ValueFormatInt, 8, 6),
		colAttr("running", "Running", "runningCount", ValueFormatInt, 8, 6),
		colAttr("pending", "Pending", "pendingCount", ValueFormatInt, 8, 6),
		colAttr("cpu", "CPU", "cpu", ValueFormatText, 8, 6),
		colAttr("memory_gb", "MemoryGb", "memoryGb", ValueFormatFloat, 9, 6),
		colCreated(), colID(),
	),
	"ecs:task": preset("ecs", "ecs:task",
		colName(), colType(), colRegion(), colStatus("lastStatus"),
		colAttr("desired", "Desired", "desiredStatus", ValueFormatText, 10, 7),
		colAttr("launch_type", "LaunchType", "launchType", ValueFormatText, 10, 7),
		colAttr("cpu", "CPU", "cpu", ValueFormatText, 8, 6),
		colAttr("memory_gb", "MemoryGb", "memoryGb", ValueFormatFloat, 9, 6),
		colCreated(), colID(),
	),
	"ecs:task-definition": preset("ecs", "ecs:task-definition",
		colName(), colType(), colRegion(),
		colAttr("family", "Family", "family", ValueFormatText, 12, 8),
		colAttr("revision", "Revision", "revision", ValueFormatInt, 8, 6),
		colAttr("compatibilities", "Compatibilities", "compatibilities", ValueFormatListCount, 14, 10),
		colAttr("cpu", "CPU", "cpu", ValueFormatText, 8, 6),
		colAttr("memory_gb", "MemoryGb", "memoryGb", ValueFormatFloat, 9, 6),
		colCreated(), colID(),
	),
	"elbv2:load-balancer": preset("elbv2", "elbv2:load-balancer",
		colName(), colType(), colRegion(), colStatus("state"),
		colAttr("lb_type", "LBType", "type", ValueFormatText, 8, 6),
		colAttr("scheme", "Scheme", "scheme", ValueFormatText, 10, 7),
		colAttr("dns", "DNS", "dnsName", ValueFormatText, 24, 10),
		colCreated(), colID(),
	),
	"elbv2:target-group": preset("elbv2", "elbv2:target-group",
		colName(), colType(), colRegion(),
		colAttr("target_type", "TargetType", "targetType", ValueFormatText, 11, 8),
		colAttr("protocol", "Protocol", "protocol", ValueFormatText, 9, 6),
		colAttr("port", "Port", "port", ValueFormatInt, 6, 4),
		colAttr("vpc", "VPC", "vpc", ValueFormatText, 12, 8),
		colCreated(), colID(),
	),
	"elbv2:listener": preset("elbv2", "elbv2:listener",
		colName(), colType(), colRegion(),
		colAttr("protocol", "Protocol", "protocol", ValueFormatText, 9, 6),
		colAttr("port", "Port", "port", ValueFormatInt, 6, 4),
		colCreated(), colID(),
	),
	"elbv2:rule": preset("elbv2", "elbv2:rule",
		colName(), colType(), colRegion(),
		colAttr("priority", "Priority", "priority", ValueFormatText, 8, 6),
		colAttr("conditions", "Conditions", "conditions", ValueFormatInt, 10, 7),
		colAttr("actions", "Actions", "actions", ValueFormatInt, 8, 6),
		colAttr("target_groups", "TargetGroups", "targetGroups", ValueFormatInt, 12, 8),
		colCreated(), colID(),
	),
	"iam:user": preset("iam", "iam:user",
		colName(), colType(), colRegion(),
		colAttr("console_access", "ConsoleAccess", "console_access", ValueFormatText, 13, 9),
		colAttr("mfa", "MFA", "mfa_active", ValueFormatBool, 6, 4),
		colAttr("groups", "Groups", "groups_count", ValueFormatInt, 8, 6),
		colAttr("access_keys", "AccessKeys", "access_keys_count", ValueFormatInt, 10, 7),
		colCreated(), colID(),
	),
	"iam:group": preset("iam", "iam:group", colName(), colType(), colRegion(), colAttr("path", "Path", "path", ValueFormatText, 16, 10), colCreated(), colID()),
	"iam:access-key": preset("iam", "iam:access-key",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("age", "Age", "age_days", ValueFormatAgeDays, 6, 4),
		colAttr("last_used", "LastUsed", "last_used_at", ValueFormatDateTime, 16, 10),
		colAttr("last_used_region", "LastUsedRegion", "last_used_region", ValueFormatText, 13, 9),
		colCreated(), colID(),
	),
	"iam:role":   preset("iam", "iam:role", colName(), colType(), colRegion(), colAttr("path", "Path", "path", ValueFormatText, 16, 10), colCreated(), colID()),
	"iam:policy": preset("iam", "iam:policy", colName(), colType(), colRegion(), colCreated(), colID()),
	"identitycenter:instance": preset("identitycenter", "identitycenter:instance",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("identity_store", "IdentityStore", "identityStoreId", ValueFormatText, 18, 10),
		colAttr("owner", "Owner", "ownerAccountId", ValueFormatText, 12, 8),
		colCreated(), colID(),
	),
	"identitycenter:permission-set": preset("identitycenter", "identitycenter:permission-set",
		colName(), colType(), colRegion(),
		colAttr("managed_policies", "ManagedPolicies", "managedPolicies", ValueFormatInt, 14, 10),
		colAttr("customer_policies", "CustomerPolicies", "customerManagedPolicies", ValueFormatInt, 15, 10),
		colAttr("session_duration", "SessionDuration", "sessionDuration", ValueFormatText, 15, 10),
		colCreated(), colID(),
	),
	"identitycenter:assignment": preset("identitycenter", "identitycenter:assignment",
		colName(), colType(), colRegion(),
		colAttr("principal_type", "PrincipalType", "principal_type", ValueFormatText, 12, 8),
		colAttr("principal_id", "PrincipalId", "principal_id", ValueFormatText, 12, 8),
		colAttr("account_id", "AccountId", "account_id", ValueFormatText, 12, 8),
		colCreated(), colID(),
	),
	"identitycenter:user":  preset("identitycenter", "identitycenter:user", colName(), colType(), colRegion(), colStatus(""), colAttr("user_name", "UserName", "userName", ValueFormatText, 16, 10), colCreated(), colID()),
	"identitycenter:group": preset("identitycenter", "identitycenter:group", colName(), colType(), colRegion(), colAttr("description", "Description", "description", ValueFormatText, 24, 10), colCreated(), colID()),
	"kms:key":              preset("kms", "kms:key", colName(), colType(), colRegion(), colStatus("keyState"), colAttr("manager", "Manager", "manager", ValueFormatText, 9, 7), colAttr("origin", "Origin", "origin", ValueFormatText, 8, 6), colCreated(), colID()),
	"kms:alias":            preset("kms", "kms:alias", colName(), colType(), colRegion(), colCreated(), colID()),
	"rds:db-instance": preset("rds", "rds:db-instance",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("class", "Class", "class", ValueFormatText, 10, 7),
		colAttr("engine", "Engine", "engine", ValueFormatText, 10, 7),
		colAttr("public", "Public", "public", ValueFormatBool, 7, 5),
		colAttr("encrypted", "Encrypted", "encrypted", ValueFormatBool, 9, 6),
		colAttr("storage_gb", "StorageGb", "allocatedStorageGb", ValueFormatInt, 10, 7),
		colCreated(), colID(),
	),
	"rds:db-cluster": preset("rds", "rds:db-cluster",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("engine", "Engine", "engine", ValueFormatText, 10, 7),
		colAttr("engine_mode", "EngineMode", "engineMode", ValueFormatText, 11, 8),
		colAttr("min_acu", "MinACU", "serverlessV2MinAcu|serverlessV1MinAcu", ValueFormatFloat, 8, 6),
		colAttr("max_acu", "MaxACU", "serverlessV2MaxAcu|serverlessV1MaxAcu", ValueFormatFloat, 8, 6),
		colCreated(), colID(),
	),
	"rds:db-subnet-group": preset("rds", "rds:db-subnet-group", colName(), colType(), colRegion(), colCreated(), colID()),
	"redshift:cluster": preset("redshift", "redshift:cluster",
		colName(), colType(), colRegion(), colStatus(""),
		colAttr("node_type", "NodeType", "nodeType", ValueFormatText, 12, 8),
		colAttr("public", "Public", "public", ValueFormatBool, 7, 5),
		colAttr("encrypted", "Encrypted", "encrypted", ValueFormatBool, 9, 6),
		colCreated(), colID(),
	),
	"redshift:subnet-group":             preset("redshift", "redshift:subnet-group", colName(), colType(), colRegion(), colStatus(""), colAttr("vpc", "VPC", "vpcId", ValueFormatText, 12, 8), colCreated(), colID()),
	"sagemaker:endpoint":                preset("sagemaker", "sagemaker:endpoint", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"sagemaker:endpoint-config":         preset("sagemaker", "sagemaker:endpoint-config", colName(), colType(), colRegion(), colCreated(), colID()),
	"sagemaker:model":                   preset("sagemaker", "sagemaker:model", colName(), colType(), colRegion(), colCreated(), colID()),
	"sagemaker:notebook-instance":       preset("sagemaker", "sagemaker:notebook-instance", colName(), colType(), colRegion(), colStatus(""), colAttr("instance_type", "InstanceType", "instanceType", ValueFormatText, 12, 8), colCreated(), colID()),
	"sagemaker:training-job":            preset("sagemaker", "sagemaker:training-job", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"sagemaker:processing-job":          preset("sagemaker", "sagemaker:processing-job", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"sagemaker:transform-job":           preset("sagemaker", "sagemaker:transform-job", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"sagemaker:domain":                  preset("sagemaker", "sagemaker:domain", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"sagemaker:user-profile":            preset("sagemaker", "sagemaker:user-profile", colName(), colType(), colRegion(), colStatus(""), colAttr("domain_id", "DomainId", "domainId", ValueFormatText, 12, 8), colCreated(), colID()),
	"securityhub:standard-subscription": preset("securityhub", "securityhub:standard-subscription", colName(), colType(), colRegion(), colStatus(""), colCreated(), colID()),
	"sns:topic":                         preset("sns", "sns:topic", colName(), colType(), colRegion(), colCreated(), colID()),
	"sns:subscription":                  preset("sns", "sns:subscription", colName(), colType(), colRegion(), colAttr("protocol", "Protocol", "protocol", ValueFormatText, 10, 7), colAttr("endpoint", "Endpoint", "endpoint", ValueFormatText, 24, 10), colCreated(), colID()),
	"sqs:queue":                         preset("sqs", "sqs:queue", colName(), colType(), colRegion(), colAttr("url", "URL", "url", ValueFormatText, 24, 10), colCreated(), colID()),
	"logs:log-group": preset("logs", "logs:log-group",
		colName(), colType(), colRegion(),
		colAttr("class", "Class", "class", ValueFormatText, 9, 6),
		colAttr("retention", "Retention", "retentionDays", ValueFormatInt, 10, 7),
		colAttr("stored", "Stored", "storedGiB|storedBytes", ValueFormatBytesGiB, 10, 8),
		colCreated(), colID(),
	),
}

// ResourceTablePreset returns the table-column preset for a service/type.
//
// Resolution order:
//  1. type override
//  2. service default
//  3. global fallback
func ResourceTablePreset(service, typ string) TablePreset {
	svc := normalizeID(service)
	t := strings.ToLower(strings.TrimSpace(typ))

	if p, ok := typeOverrides[t]; ok {
		out := clonePreset(p)
		if out.Service == "" {
			out.Service = svc
		}
		if out.Type == "" {
			out.Type = t
		}
		return out
	}
	if p, ok := serviceDefaults[svc]; ok {
		out := clonePreset(p)
		if out.Service == "" {
			out.Service = svc
		}
		if out.Type == "" {
			out.Type = t
		}
		return out
	}
	out := clonePreset(globalTablePreset)
	out.Service = svc
	out.Type = t
	return out
}
