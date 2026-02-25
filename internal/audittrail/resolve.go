package audittrail

import (
	"encoding/json"
	"fmt"
	"strings"

	"awscope/internal/graph"
	"awscope/internal/store"
)

type ResolveInput struct {
	Service   string
	Region    string
	EventName string
	Event     map[string]any
	Resources []EventResource
}

type ResolveResult struct {
	Key          graph.ResourceKey
	ResourceType string
	ResourceName string
	ResourceArn  string
}

type EventResource struct {
	ResourceType string
	ResourceName string
	ResourceArn  string
}

type resourceResolver struct {
	partition string
	accountID string

	byARN         map[string]store.ResourceLookup
	byTypeRegion  map[string]store.ResourceLookup
	byType        map[string][]store.ResourceLookup
	byServiceType map[string][]store.ResourceLookup
}

func newResourceResolver(partition, accountID string, refs []store.ResourceLookup) *resourceResolver {
	r := &resourceResolver{
		partition:     strings.TrimSpace(partition),
		accountID:     strings.TrimSpace(accountID),
		byARN:         map[string]store.ResourceLookup{},
		byTypeRegion:  map[string]store.ResourceLookup{},
		byType:        map[string][]store.ResourceLookup{},
		byServiceType: map[string][]store.ResourceLookup{},
	}
	for _, ref := range refs {
		if a := strings.ToLower(strings.TrimSpace(ref.Arn)); a != "" {
			r.byARN[a] = ref
		}
		if ref.Type != "" && ref.PrimaryID != "" {
			r.byTypeRegion[keyTypeRegionPrimary(ref.Type, ref.Region, ref.PrimaryID)] = ref
			r.byType[ref.Type] = append(r.byType[ref.Type], ref)
			service := serviceFromType(ref.Type)
			r.byServiceType[service+"|"+ref.Type] = append(r.byServiceType[service+"|"+ref.Type], ref)
		}
	}
	return r
}

func (r *resourceResolver) Resolve(in ResolveInput) ResolveResult {
	cands := r.candidates(in)
	for _, c := range cands {
		if ref, ok := r.lookupCandidate(c); ok {
			return ResolveResult{
				Key:          ref.Key,
				ResourceType: ref.Type,
				ResourceName: firstNonEmpty(ref.DisplayName, c.name),
				ResourceArn:  firstNonEmpty(ref.Arn, c.arn),
			}
		}
	}
	if len(cands) == 0 {
		return ResolveResult{}
	}
	c := cands[0]
	return ResolveResult{
		ResourceType: c.typ,
		ResourceName: c.name,
		ResourceArn:  c.arn,
	}
}

type candidate struct {
	typ       string
	service   string
	region    string
	primaryID string
	arn       string
	name      string
}

func (r *resourceResolver) candidates(in ResolveInput) []candidate {
	service := strings.TrimSpace(in.Service)
	region := strings.TrimSpace(in.Region)
	var out []candidate
	seen := map[string]struct{}{}
	add := func(c candidate) {
		c.typ = strings.TrimSpace(c.typ)
		c.service = strings.TrimSpace(c.service)
		c.region = strings.TrimSpace(c.region)
		c.primaryID = strings.TrimSpace(c.primaryID)
		c.arn = strings.TrimSpace(c.arn)
		c.name = strings.TrimSpace(c.name)
		if c.typ == "" && c.arn == "" && c.primaryID == "" {
			return
		}
		k := strings.Join([]string{c.typ, c.region, c.primaryID, strings.ToLower(c.arn), c.name}, "|")
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, c)
	}

	for _, er := range in.Resources {
		c := candidate{
			service: service,
			region:  region,
			typ:     mapResourceType(service, er.ResourceType),
			name:    er.ResourceName,
			arn:     er.ResourceArn,
		}
		if c.arn == "" && strings.HasPrefix(strings.TrimSpace(er.ResourceName), "arn:") {
			c.arn = strings.TrimSpace(er.ResourceName)
		}
		if c.arn != "" {
			parsed := parseARNCandidate(c.arn)
			if c.typ == "" {
				c.typ = parsed.typ
			}
			if c.region == "" || c.region == "global" {
				c.region = firstNonEmpty(parsed.region, c.region)
			}
			if c.primaryID == "" {
				c.primaryID = parsed.primaryID
			}
			if c.service == "" {
				c.service = serviceFromType(parsed.typ)
			}
		}
		if c.primaryID == "" {
			c.primaryID = c.name
		}
		add(c)
	}

	for _, c := range fallbackCandidates(service, region, in.EventName, in.Event) {
		add(c)
	}
	return out
}

func (r *resourceResolver) lookupCandidate(c candidate) (store.ResourceLookup, bool) {
	if c.arn != "" {
		if ref, ok := r.byARN[strings.ToLower(strings.TrimSpace(c.arn))]; ok {
			return ref, true
		}
	}
	if c.typ == "" || c.primaryID == "" {
		return store.ResourceLookup{}, false
	}

	// Exact region/type/id.
	if ref, ok := r.byTypeRegion[keyTypeRegionPrimary(c.typ, c.region, c.primaryID)]; ok {
		return ref, true
	}
	// Try global as fallback.
	if ref, ok := r.byTypeRegion[keyTypeRegionPrimary(c.typ, "global", c.primaryID)]; ok {
		return ref, true
	}
	// Fuzzy fallback for ARN-backed primary IDs where CloudTrail reports name/id.
	for _, ref := range r.byType[c.typ] {
		if primaryMatches(ref.PrimaryID, c.primaryID) {
			if c.region == "" || ref.Region == c.region || ref.Region == "global" {
				return ref, true
			}
		}
	}
	return store.ResourceLookup{}, false
}

func keyTypeRegionPrimary(typ, region, primary string) string {
	return strings.ToLower(strings.TrimSpace(typ)) + "|" + strings.ToLower(strings.TrimSpace(region)) + "|" + strings.TrimSpace(primary)
}

func primaryMatches(stored, candidate string) bool {
	stored = strings.TrimSpace(stored)
	candidate = strings.TrimSpace(candidate)
	if stored == "" || candidate == "" {
		return false
	}
	if stored == candidate {
		return true
	}
	lowStored := strings.ToLower(stored)
	lowCandidate := strings.ToLower(candidate)
	if lowStored == lowCandidate {
		return true
	}
	if strings.HasSuffix(lowStored, "/"+lowCandidate) || strings.HasSuffix(lowStored, ":"+lowCandidate) {
		return true
	}
	return false
}

func mapResourceType(service, ctType string) string {
	service = strings.ToLower(strings.TrimSpace(service))
	t := strings.ToLower(strings.TrimSpace(ctType))
	t = strings.TrimPrefix(t, "aws::")
	switch service {
	case "ec2":
		switch {
		case strings.Contains(t, "instance"):
			return "ec2:instance"
		case strings.Contains(t, "volume"):
			return "ec2:volume"
		case strings.Contains(t, "snapshot"):
			return "ec2:snapshot"
		case strings.Contains(t, "securitygroup"):
			return "ec2:security-group"
		case strings.Contains(t, "subnet"):
			return "ec2:subnet"
		case strings.Contains(t, "vpc"):
			return "ec2:vpc"
		case strings.Contains(t, "networkinterface"):
			return "ec2:network-interface"
		case strings.Contains(t, "routetable"):
			return "ec2:route-table"
		case strings.Contains(t, "natgateway"):
			return "ec2:nat-gateway"
		case strings.Contains(t, "internetgateway"):
			return "ec2:internet-gateway"
		}
	case "iam":
		switch {
		case strings.Contains(t, "role"):
			return "iam:role"
		case strings.Contains(t, "user"):
			return "iam:user"
		case strings.Contains(t, "group"):
			return "iam:group"
		case strings.Contains(t, "policy"):
			return "iam:policy"
		case strings.Contains(t, "accesskey"):
			return "iam:access-key"
		}
	case "ecs":
		switch {
		case strings.Contains(t, "service"):
			return "ecs:service"
		case strings.Contains(t, "cluster"):
			return "ecs:cluster"
		case strings.Contains(t, "taskdefinition"):
			return "ecs:task-definition"
		case strings.Contains(t, "task"):
			return "ecs:task"
		}
	case "elbv2":
		switch {
		case strings.Contains(t, "loadbalancer"):
			return "elbv2:load-balancer"
		case strings.Contains(t, "targetgroup"):
			return "elbv2:target-group"
		case strings.Contains(t, "listener"):
			return "elbv2:listener"
		case strings.Contains(t, "rule"):
			return "elbv2:rule"
		}
	case "autoscaling":
		if strings.Contains(t, "autoscalinggroup") {
			return "autoscaling:group"
		}
	case "rds":
		switch {
		case strings.Contains(t, "dbinstance"):
			return "rds:db-instance"
		case strings.Contains(t, "dbcluster"):
			return "rds:db-cluster"
		}
	case "lambda":
		if strings.Contains(t, "function") {
			return "lambda:function"
		}
	case "s3":
		if strings.Contains(t, "bucket") {
			return "s3:bucket"
		}
	case "kms":
		switch {
		case strings.Contains(t, "key"):
			return "kms:key"
		case strings.Contains(t, "alias"):
			return "kms:alias"
		}
	case "secretsmanager":
		if strings.Contains(t, "secret") {
			return "secretsmanager:secret"
		}
	}
	return ""
}

func parseARNCandidate(arn string) candidate {
	arn = strings.TrimSpace(arn)
	if arn == "" || !strings.HasPrefix(arn, "arn:") {
		return candidate{}
	}
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return candidate{}
	}
	service := strings.TrimSpace(parts[2])
	region := strings.TrimSpace(parts[3])
	resource := strings.TrimSpace(parts[5])
	c := candidate{arn: arn, region: region}

	switch service {
	case "ec2":
		if i := strings.Index(resource, "/"); i > 0 && i+1 < len(resource) {
			kind := resource[:i]
			id := resource[i+1:]
			switch kind {
			case "instance":
				c.typ = "ec2:instance"
			case "volume":
				c.typ = "ec2:volume"
			case "snapshot":
				c.typ = "ec2:snapshot"
			case "image":
				c.typ = "ec2:ami"
			case "vpc":
				c.typ = "ec2:vpc"
			case "subnet":
				c.typ = "ec2:subnet"
			case "security-group":
				c.typ = "ec2:security-group"
			case "network-interface":
				c.typ = "ec2:network-interface"
			case "internet-gateway":
				c.typ = "ec2:internet-gateway"
			case "natgateway":
				c.typ = "ec2:nat-gateway"
			case "route-table":
				c.typ = "ec2:route-table"
			case "network-acl":
				c.typ = "ec2:nacl"
			case "launch-template":
				c.typ = "ec2:launch-template"
			case "key-pair":
				c.typ = "ec2:key-pair"
			case "placement-group":
				c.typ = "ec2:placement-group"
			}
			c.primaryID = strings.TrimSpace(id)
		}
	case "iam":
		c.region = "global"
		c.primaryID = arn
		if i := strings.Index(resource, "/"); i > 0 {
			switch resource[:i] {
			case "role":
				c.typ = "iam:role"
			case "user":
				c.typ = "iam:user"
			case "group":
				c.typ = "iam:group"
			case "policy":
				c.typ = "iam:policy"
			}
			c.name = resource[i+1:]
		}
	case "ecs":
		c.primaryID = arn
		if i := strings.Index(resource, "/"); i > 0 {
			switch resource[:i] {
			case "cluster":
				c.typ = "ecs:cluster"
			case "service":
				c.typ = "ecs:service"
			case "task-definition":
				c.typ = "ecs:task-definition"
			case "task":
				c.typ = "ecs:task"
			}
			c.name = resource[i+1:]
		}
	case "elasticloadbalancing":
		c.primaryID = arn
		switch {
		case strings.HasPrefix(resource, "loadbalancer/"):
			c.typ = "elbv2:load-balancer"
		case strings.HasPrefix(resource, "targetgroup/"):
			c.typ = "elbv2:target-group"
		case strings.HasPrefix(resource, "listener/"):
			c.typ = "elbv2:listener"
		case strings.HasPrefix(resource, "rule/"):
			c.typ = "elbv2:rule"
		}
	case "autoscaling":
		c.primaryID = arn
		c.typ = "autoscaling:group"
	case "rds":
		c.primaryID = arn
		switch {
		case strings.HasPrefix(resource, "db:"):
			c.typ = "rds:db-instance"
			c.name = strings.TrimPrefix(resource, "db:")
		case strings.HasPrefix(resource, "cluster:"):
			c.typ = "rds:db-cluster"
			c.name = strings.TrimPrefix(resource, "cluster:")
		}
	case "lambda":
		c.primaryID = arn
		c.typ = "lambda:function"
	case "s3":
		c.region = firstNonEmpty(region, "global")
		c.typ = "s3:bucket"
		c.primaryID = resource
		c.name = resource
	case "kms":
		c.primaryID = arn
		switch {
		case strings.HasPrefix(resource, "key/"):
			c.typ = "kms:key"
		case strings.HasPrefix(resource, "alias/"):
			c.typ = "kms:alias"
		}
	case "secretsmanager":
		c.primaryID = arn
		c.typ = "secretsmanager:secret"
	}
	if c.name == "" {
		c.name = c.primaryID
	}
	return c
}

func fallbackCandidates(service, region, eventName string, ev map[string]any) []candidate {
	var out []candidate
	add := func(c candidate) {
		c.service = service
		c.region = firstNonEmpty(c.region, region)
		if c.typ == "" && c.primaryID == "" && c.arn == "" {
			return
		}
		out = append(out, c)
	}

	addArn := func(arn string) {
		if strings.TrimSpace(arn) == "" {
			return
		}
		add(parseARNCandidate(arn))
	}

	switch service {
	case "ec2":
		for _, id := range findInstanceIDs(eventName, ev) {
			add(candidate{typ: "ec2:instance", primaryID: id, name: id})
		}
		add(candidate{typ: "ec2:volume", primaryID: strAt(ev, "responseElements", "volumeId"), name: strAt(ev, "responseElements", "volumeId")})
		add(candidate{typ: "ec2:snapshot", primaryID: strAt(ev, "responseElements", "snapshotId"), name: strAt(ev, "responseElements", "snapshotId")})
		add(candidate{typ: "ec2:vpc", primaryID: firstNonEmpty(strAt(ev, "responseElements", "vpc", "vpcId"), strAt(ev, "requestParameters", "vpcId")), name: firstNonEmpty(strAt(ev, "responseElements", "vpc", "vpcId"), strAt(ev, "requestParameters", "vpcId"))})
		add(candidate{typ: "ec2:subnet", primaryID: firstNonEmpty(strAt(ev, "responseElements", "subnet", "subnetId"), strAt(ev, "requestParameters", "subnetId")), name: firstNonEmpty(strAt(ev, "responseElements", "subnet", "subnetId"), strAt(ev, "requestParameters", "subnetId"))})
		add(candidate{typ: "ec2:security-group", primaryID: firstNonEmpty(strAt(ev, "responseElements", "groupId"), strAt(ev, "requestParameters", "groupId")), name: firstNonEmpty(strAt(ev, "requestParameters", "groupName"), strAt(ev, "responseElements", "groupId"), strAt(ev, "requestParameters", "groupId"))})
		add(candidate{typ: "ec2:network-interface", primaryID: firstNonEmpty(strAt(ev, "responseElements", "networkInterface", "networkInterfaceId"), strAt(ev, "requestParameters", "networkInterfaceId")), name: firstNonEmpty(strAt(ev, "responseElements", "networkInterface", "networkInterfaceId"), strAt(ev, "requestParameters", "networkInterfaceId"))})
	case "iam":
		addArn(strAt(ev, "responseElements", "role", "arn"))
		addArn(strAt(ev, "responseElements", "user", "arn"))
		addArn(strAt(ev, "responseElements", "group", "arn"))
		addArn(strAt(ev, "requestParameters", "policyArn"))
		if name := strAt(ev, "requestParameters", "roleName"); name != "" {
			add(candidate{typ: "iam:role", primaryID: "arn:aws:iam::" + strAt(ev, "recipientAccountId") + ":role/" + name, name: name, region: "global"})
		}
		if name := strAt(ev, "requestParameters", "userName"); name != "" {
			add(candidate{typ: "iam:user", primaryID: "arn:aws:iam::" + strAt(ev, "recipientAccountId") + ":user/" + name, name: name, region: "global"})
		}
		if name := strAt(ev, "requestParameters", "groupName"); name != "" {
			add(candidate{typ: "iam:group", primaryID: "arn:aws:iam::" + strAt(ev, "recipientAccountId") + ":group/" + name, name: name, region: "global"})
		}
		if keyID := firstNonEmpty(strAt(ev, "responseElements", "accessKey", "accessKeyId"), strAt(ev, "requestParameters", "accessKeyId")); keyID != "" {
			add(candidate{typ: "iam:access-key", primaryID: keyID, name: keyID, region: "global"})
		}
	case "ecs":
		addArn(firstNonEmpty(strAt(ev, "responseElements", "service", "serviceArn"), strAt(ev, "requestParameters", "service")))
		addArn(firstNonEmpty(strAt(ev, "responseElements", "cluster", "clusterArn"), strAt(ev, "requestParameters", "cluster")))
		addArn(firstNonEmpty(strAt(ev, "responseElements", "taskDefinition", "taskDefinitionArn"), strAt(ev, "requestParameters", "taskDefinition")))
	case "elbv2":
		for _, arn := range listAtStrings(ev, "responseElements", "loadBalancers", "loadBalancerArn") {
			addArn(arn)
		}
		for _, arn := range listAtStrings(ev, "responseElements", "targetGroups", "targetGroupArn") {
			addArn(arn)
		}
		addArn(strAt(ev, "requestParameters", "loadBalancerArn"))
		addArn(strAt(ev, "requestParameters", "targetGroupArn"))
	case "autoscaling":
		addArn(strAt(ev, "responseElements", "autoScalingGroupARN"))
		if n := strAt(ev, "requestParameters", "autoScalingGroupName"); n != "" {
			add(candidate{typ: "autoscaling:group", primaryID: n, name: n})
		}
	case "rds":
		addArn(firstNonEmpty(strAt(ev, "responseElements", "dBInstanceArn"), strAt(ev, "responseElements", "dBInstance", "dBInstanceArn")))
		addArn(firstNonEmpty(strAt(ev, "responseElements", "dBClusterArn"), strAt(ev, "responseElements", "dBCluster", "dBClusterArn")))
		if id := strAt(ev, "requestParameters", "dBInstanceIdentifier"); id != "" {
			add(candidate{typ: "rds:db-instance", primaryID: id, name: id})
		}
		if id := strAt(ev, "requestParameters", "dBClusterIdentifier"); id != "" {
			add(candidate{typ: "rds:db-cluster", primaryID: id, name: id})
		}
	case "lambda":
		addArn(firstNonEmpty(strAt(ev, "responseElements", "functionArn"), strAt(ev, "requestParameters", "functionName")))
		if fn := strAt(ev, "requestParameters", "functionName"); fn != "" {
			add(candidate{typ: "lambda:function", primaryID: fn, name: fn})
		}
	case "s3":
		if b := strAt(ev, "requestParameters", "bucketName"); b != "" {
			add(candidate{typ: "s3:bucket", primaryID: b, name: b, arn: "arn:aws:s3:::" + b})
		}
	case "kms":
		addArn(firstNonEmpty(strAt(ev, "responseElements", "keyMetadata", "arn"), strAt(ev, "requestParameters", "targetKeyId")))
	case "secretsmanager":
		addArn(firstNonEmpty(strAt(ev, "responseElements", "arn"), strAt(ev, "requestParameters", "secretId")))
		if n := strAt(ev, "requestParameters", "name"); n != "" {
			add(candidate{typ: "secretsmanager:secret", primaryID: n, name: n})
		}
	}
	return out
}

func findInstanceIDs(eventName string, ev map[string]any) []string {
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		out = append(out, v)
	}
	for _, id := range listAtStrings(ev, "requestParameters", "instancesSet", "items", "instanceId") {
		add(id)
	}
	for _, id := range listAtStrings(ev, "responseElements", "instancesSet", "items", "instanceId") {
		add(id)
	}
	if len(out) > 0 {
		return dedupeStrings(out)
	}
	if strings.EqualFold(strings.TrimSpace(eventName), "TerminateInstances") {
		if id := strAt(ev, "requestParameters", "instanceId"); id != "" {
			add(id)
		}
	}
	return dedupeStrings(out)
}

func serviceFromType(typ string) string {
	typ = strings.TrimSpace(typ)
	if typ == "" {
		return ""
	}
	parts := strings.SplitN(typ, ":", 2)
	return parts[0]
}

func strAt(m map[string]any, path ...string) string {
	v := anyAt(m, path...)
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return strings.TrimSpace(x.String())
	case float64:
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%v", x), ".0"), "."))
	default:
		return ""
	}
}

func listAtStrings(m map[string]any, path ...string) []string {
	// Supports either direct []any path or []map path with final field name.
	if len(path) == 0 {
		return nil
	}
	leaf := path[len(path)-1]
	parent := path[:len(path)-1]
	v := anyAt(m, parent...)
	if vv := anyAt(m, path...); vv != nil {
		if arr, ok := vv.([]any); ok {
			out := make([]string, 0, len(arr))
			for _, it := range arr {
				if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
					out = append(out, strings.TrimSpace(s))
				}
			}
			return dedupeStrings(out)
		}
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, it := range arr {
		obj, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if s, ok := obj[leaf].(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return dedupeStrings(out)
}

func anyAt(m map[string]any, path ...string) any {
	if m == nil {
		return nil
	}
	var cur any = m
	for _, p := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = obj[p]
		if !ok {
			return nil
		}
	}
	return cur
}

func dedupeStrings(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}
