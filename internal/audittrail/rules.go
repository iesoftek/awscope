package audittrail

import (
	"sort"
	"strings"
)

var serviceAllowlist = map[string]string{
	"iam.amazonaws.com":                  "iam",
	"ec2.amazonaws.com":                  "ec2",
	"ecs.amazonaws.com":                  "ecs",
	"elasticloadbalancing.amazonaws.com": "elbv2",
	"autoscaling.amazonaws.com":          "autoscaling",
	"rds.amazonaws.com":                  "rds",
	"lambda.amazonaws.com":               "lambda",
	"s3.amazonaws.com":                   "s3",
	"kms.amazonaws.com":                  "kms",
	"secretsmanager.amazonaws.com":       "secretsmanager",
}

var actionByEventName = map[string]string{
	"runinstances":             "create",
	"terminateinstances":       "delete",
	"registertaskdefinition":   "create",
	"deregistertaskdefinition": "delete",
}

func normalizeService(eventSource string) (string, bool) {
	service, ok := serviceAllowlist[strings.ToLower(strings.TrimSpace(eventSource))]
	return service, ok
}

func allowedEventSources() []string {
	out := make([]string, 0, len(serviceAllowlist))
	for k := range serviceAllowlist {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func classifyAction(eventName string) (string, bool) {
	name := strings.TrimSpace(eventName)
	if name == "" {
		return "", false
	}
	lower := strings.ToLower(name)
	if v, ok := actionByEventName[lower]; ok {
		return v, true
	}
	switch {
	case strings.HasPrefix(lower, "create"):
		return "create", true
	case strings.HasPrefix(lower, "delete"):
		return "delete", true
	}
	return "", false
}
