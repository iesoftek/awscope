package security

import (
	"encoding/json"
	"sort"
	"strings"

	"awscope/internal/graph"
)

const (
	defaultMaxKeyAgeDays = 90
	maxSamplesPerFinding = 5
)

type evalContext struct {
	nodesByType    map[string][]graph.ResourceNode
	regionScope    []string
	scannedService map[string]struct{}
	maxKeyAgeDays  int
}

type resourceRef struct {
	name   string
	region string
}

func Evaluate(in EvaluateInput) Summary {
	maxKeyAge := in.MaxKeyAgeDays
	if maxKeyAge <= 0 {
		maxKeyAge = defaultMaxKeyAgeDays
	}

	scanned := normalizeServiceSet(in.ScannedServices)
	nodesByType := map[string][]graph.ResourceNode{}
	for _, n := range in.Nodes {
		t := strings.TrimSpace(strings.ToLower(n.Type))
		if t == "" {
			continue
		}
		nodesByType[t] = append(nodesByType[t], n)
	}

	ctx := &evalContext{
		nodesByType:    nodesByType,
		regionScope:    deriveRegionScope(in.SelectedRegions, in.Nodes),
		scannedService: scanned,
		maxKeyAgeDays:  maxKeyAge,
	}

	out := Summary{
		AffectedBySeverity: map[Severity]int{
			SeverityCritical: 0,
			SeverityHigh:     0,
			SeverityMedium:   0,
			SeverityLow:      0,
		},
	}

	var findings []Finding
	missingServices := map[string]struct{}{}

	for _, r := range rules() {
		if !coveredByScannedServices(scanned, r.requiredServices) {
			out.Coverage.SkippedChecks++
			for _, svc := range r.requiredServices {
				svc = strings.TrimSpace(strings.ToLower(svc))
				if svc != "" {
					missingServices[svc] = struct{}{}
				}
			}
			continue
		}
		out.Coverage.AssessedChecks++
		f, ok := r.eval(ctx, r)
		if !ok || f == nil || f.AffectedCount <= 0 {
			continue
		}
		findings = append(findings, *f)
		out.AffectedBySeverity[f.Severity] += f.AffectedCount
	}

	sort.Slice(findings, func(i, j int) bool {
		ri := severityRank(findings[i].Severity)
		rj := severityRank(findings[j].Severity)
		if ri != rj {
			return ri > rj
		}
		if findings[i].AffectedCount != findings[j].AffectedCount {
			return findings[i].AffectedCount > findings[j].AffectedCount
		}
		return findings[i].CheckID < findings[j].CheckID
	})
	out.Findings = findings

	for svc := range missingServices {
		out.Coverage.MissingServices = append(out.Coverage.MissingServices, svc)
	}
	sort.Strings(out.Coverage.MissingServices)

	return out
}

func coveredByScannedServices(scanned map[string]struct{}, required []string) bool {
	if len(required) == 0 {
		return true
	}
	if len(scanned) == 0 {
		return false
	}
	for _, svc := range required {
		svc = strings.TrimSpace(strings.ToLower(svc))
		if svc == "" {
			continue
		}
		if _, ok := scanned[svc]; !ok {
			return false
		}
	}
	return true
}

func normalizeServiceSet(svcs []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, svc := range svcs {
		svc = strings.TrimSpace(strings.ToLower(svc))
		if svc == "" {
			continue
		}
		out[svc] = struct{}{}
	}
	return out
}

func deriveRegionScope(selected []string, nodes []graph.ResourceNode) []string {
	set := map[string]struct{}{}
	for _, region := range selected {
		region = strings.TrimSpace(region)
		if region == "" || strings.EqualFold(region, "global") {
			continue
		}
		set[region] = struct{}{}
	}
	if len(set) == 0 {
		for _, n := range nodes {
			r := strings.TrimSpace(nodeRegion(n))
			if r == "" || strings.EqualFold(r, "global") {
				continue
			}
			set[r] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0
	}
}

func nodeRegion(n graph.ResourceNode) string {
	_, _, region, _, _, err := graph.ParseResourceKey(n.Key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(region)
}

func nodeName(n graph.ResourceNode) string {
	if s := strings.TrimSpace(n.DisplayName); s != "" {
		return s
	}
	if s := strings.TrimSpace(n.PrimaryID); s != "" {
		return s
	}
	if s := strings.TrimSpace(n.Arn); s != "" {
		return s
	}
	return strings.TrimSpace(string(n.Key))
}

func findingScalar(r ruleSpec, severity Severity, affected int, regions, samples []string) *Finding {
	if affected <= 0 {
		return nil
	}
	return &Finding{
		CheckID:       r.id,
		Severity:      severity,
		Title:         r.title,
		Service:       r.service,
		ControlRef:    r.controlRef,
		GuidanceURL:   r.guidanceURL,
		AffectedCount: affected,
		Regions:       dedupeSorted(regions, 0),
		Samples:       dedupeSorted(samples, maxSamplesPerFinding),
	}
}

func findingFromRefs(r ruleSpec, severity Severity, refs []resourceRef) *Finding {
	if len(refs) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	regions := map[string]struct{}{}
	samples := map[string]struct{}{}
	affected := 0
	for _, ref := range refs {
		name := strings.TrimSpace(ref.name)
		region := strings.TrimSpace(ref.region)
		key := region + "|" + name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		affected++
		if region != "" {
			regions[region] = struct{}{}
		}
		if name != "" {
			samples[name] = struct{}{}
		}
	}
	if affected == 0 {
		return nil
	}
	regionList := make([]string, 0, len(regions))
	for region := range regions {
		regionList = append(regionList, region)
	}
	sort.Strings(regionList)

	sampleList := make([]string, 0, len(samples))
	for sample := range samples {
		sampleList = append(sampleList, sample)
	}
	sort.Strings(sampleList)
	if len(sampleList) > maxSamplesPerFinding {
		sampleList = sampleList[:maxSamplesPerFinding]
	}

	return &Finding{
		CheckID:       r.id,
		Severity:      severity,
		Title:         r.title,
		Service:       r.service,
		ControlRef:    r.controlRef,
		GuidanceURL:   r.guidanceURL,
		AffectedCount: affected,
		Regions:       regionList,
		Samples:       sampleList,
	}
}

func dedupeSorted(items []string, limit int) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func evalCTNoLoggingTrail(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	for _, n := range ctx.nodesByType["cloudtrail:trail"] {
		if strings.EqualFold(asString(n.Attributes["status"]), "logging") {
			return nil, false
		}
	}
	return findingScalar(r, r.severity, 1, ctx.regionScope, nil), true
}

func evalCTNoMultiRegionTrail(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	for _, n := range ctx.nodesByType["cloudtrail:trail"] {
		logging := strings.EqualFold(asString(n.Attributes["status"]), "logging")
		multi, ok := asBool(n.Attributes["isMultiRegionTrail"])
		if logging && ok && multi {
			return nil, false
		}
	}
	return findingScalar(r, r.severity, 1, ctx.regionScope, nil), true
}

func evalCTNoLogValidation(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	for _, n := range ctx.nodesByType["cloudtrail:trail"] {
		logging := strings.EqualFold(asString(n.Attributes["status"]), "logging")
		validation, ok := asBool(n.Attributes["logFileValidationEnabled"])
		if logging && ok && validation {
			return nil, false
		}
	}
	return findingScalar(r, r.severity, 1, ctx.regionScope, nil), true
}

func evalConfigRecorderCoverage(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	return evalRegionalCoverage(ctx, r, ctx.nodesByType["config:recorder"], func(n graph.ResourceNode) bool {
		if v, ok := asBool(n.Attributes["recording"]); ok && v {
			return true
		}
		return strings.EqualFold(asString(n.Attributes["status"]), "recording")
	})
}

func evalGuardDutyCoverage(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	return evalRegionalCoverage(ctx, r, ctx.nodesByType["guardduty:detector"], func(n graph.ResourceNode) bool {
		return strings.EqualFold(asString(n.Attributes["status"]), "enabled")
	})
}

func evalSecurityHubCoverage(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	return evalRegionalCoverage(ctx, r, ctx.nodesByType["securityhub:hub"], func(n graph.ResourceNode) bool {
		status := strings.TrimSpace(strings.ToLower(asString(n.Attributes["status"])))
		return status == "" || status == "enabled"
	})
}

func evalAccessAnalyzerCoverage(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	return evalRegionalCoverage(ctx, r, ctx.nodesByType["accessanalyzer:analyzer"], func(n graph.ResourceNode) bool {
		return strings.EqualFold(asString(n.Attributes["status"]), "active")
	})
}

func evalRegionalCoverage(ctx *evalContext, r ruleSpec, nodes []graph.ResourceNode, activeFn func(graph.ResourceNode) bool) (*Finding, bool) {
	if len(ctx.regionScope) == 0 {
		return nil, false
	}
	have := map[string]struct{}{}
	for _, n := range nodes {
		if !activeFn(n) {
			continue
		}
		region := nodeRegion(n)
		if region == "" || strings.EqualFold(region, "global") {
			continue
		}
		have[region] = struct{}{}
	}

	var missing []string
	for _, region := range ctx.regionScope {
		if _, ok := have[region]; !ok {
			missing = append(missing, region)
		}
	}
	if len(missing) == 0 {
		return nil, false
	}
	refs := make([]resourceRef, 0, len(missing))
	for _, region := range missing {
		refs = append(refs, resourceRef{name: region, region: region})
	}
	return findingFromRefs(r, r.severity, refs), true
}

func evalS3PublicAccessBlock(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	var refs []resourceRef
	for _, n := range ctx.nodesByType["s3:bucket"] {
		if s3BucketHasIncompletePAB(n.Attributes) {
			refs = append(refs, resourceRef{name: nodeName(n), region: nodeRegion(n)})
		}
	}
	return findingFromRefs(r, r.severity, refs), true
}

func s3BucketHasIncompletePAB(attrs map[string]any) bool {
	v, ok := attrs["public_access_block"]
	if !ok {
		return true
	}
	if strings.EqualFold(asString(v), "not-configured") {
		return true
	}
	m := asMap(v)
	if m == nil {
		return true
	}
	keys := []string{"block_public_acls", "ignore_public_acls", "block_public_policy", "restrict_public_buckets"}
	for _, k := range keys {
		b, ok := asBool(mapValue(m, k))
		if !ok || !b {
			return true
		}
	}
	return false
}

func evalS3Encryption(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	var refs []resourceRef
	for _, n := range ctx.nodesByType["s3:bucket"] {
		if strings.TrimSpace(asString(n.Attributes["encryption"])) == "" {
			refs = append(refs, resourceRef{name: nodeName(n), region: nodeRegion(n)})
		}
	}
	return findingFromRefs(r, r.severity, refs), true
}

func evalRDSPublic(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	var refs []resourceRef
	for _, n := range ctx.nodesByType["rds:db-instance"] {
		if b, ok := asBool(n.Attributes["public"]); ok && b {
			refs = append(refs, resourceRef{name: nodeName(n), region: nodeRegion(n)})
		}
	}
	return findingFromRefs(r, r.severity, refs), true
}

func evalRDSEncryption(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	var refs []resourceRef
	for _, n := range ctx.nodesByType["rds:db-instance"] {
		if b, ok := asBool(n.Attributes["encrypted"]); !ok || !b {
			refs = append(refs, resourceRef{name: nodeName(n), region: nodeRegion(n)})
		}
	}
	return findingFromRefs(r, r.severity, refs), true
}

func evalIAMConsoleWithoutMFA(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	var refs []resourceRef
	for _, n := range ctx.nodesByType["iam:user"] {
		passwordEnabled, ok := asBool(n.Attributes["password_enabled"])
		if !ok || !passwordEnabled {
			continue
		}
		mfa, mok := asBool(n.Attributes["mfa_active"])
		if !mok || !mfa {
			refs = append(refs, resourceRef{name: nodeName(n), region: "global"})
		}
	}
	return findingFromRefs(r, r.severity, refs), true
}

func evalIAMOldAccessKeys(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	var refs []resourceRef
	for _, n := range ctx.nodesByType["iam:access-key"] {
		if !strings.EqualFold(asString(n.Attributes["status"]), "active") {
			continue
		}
		age, ok := asInt(n.Attributes["age_days"])
		if !ok {
			continue
		}
		if age > ctx.maxKeyAgeDays {
			refs = append(refs, resourceRef{name: nodeName(n), region: "global"})
		}
	}
	return findingFromRefs(r, r.severity, refs), true
}

func evalSecretsRotation(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	var refs []resourceRef
	for _, n := range ctx.nodesByType["secretsmanager:secret"] {
		rot, ok := asBool(n.Attributes["rotationEnabled"])
		if ok && !rot {
			refs = append(refs, resourceRef{name: nodeName(n), region: nodeRegion(n)})
		}
	}
	return findingFromRefs(r, r.severity, refs), true
}

func evalEKSPublicAPI(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	var refs []resourceRef
	for _, n := range ctx.nodesByType["eks:cluster"] {
		publicAPI, ok := asBool(n.Attributes["publicApi"])
		if ok && publicAPI {
			refs = append(refs, resourceRef{name: nodeName(n), region: nodeRegion(n)})
		}
	}
	return findingFromRefs(r, r.severity, refs), true
}

func evalEC2PublicIP(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	var refs []resourceRef
	for _, n := range ctx.nodesByType["ec2:instance"] {
		state := strings.TrimSpace(strings.ToLower(asString(n.Attributes["state"])))
		if state != "running" {
			continue
		}
		if strings.TrimSpace(asString(n.Attributes["publicIp"])) != "" {
			refs = append(refs, resourceRef{name: nodeName(n), region: nodeRegion(n)})
		}
	}
	return findingFromRefs(r, r.severity, refs), true
}

func evalEC2WorldOpenIngress(ctx *evalContext, r ruleSpec) (*Finding, bool) {
	var criticalRefs []resourceRef
	var highRefs []resourceRef
	for _, n := range ctx.nodesByType["ec2:security-group"] {
		critical, high := securityGroupWorldOpenExposure(n.Raw)
		ref := resourceRef{name: nodeName(n), region: nodeRegion(n)}
		if critical {
			criticalRefs = append(criticalRefs, ref)
			continue
		}
		if high {
			highRefs = append(highRefs, ref)
		}
	}
	if len(criticalRefs) == 0 && len(highRefs) == 0 {
		return nil, false
	}
	sev := SeverityHigh
	refs := highRefs
	if len(criticalRefs) > 0 {
		sev = SeverityCritical
		refs = append(append([]resourceRef{}, criticalRefs...), highRefs...)
	}
	return findingFromRefs(r, sev, refs), true
}

func securityGroupWorldOpenExposure(raw json.RawMessage) (critical bool, high bool) {
	if len(raw) == 0 {
		return false, false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return false, false
	}
	perms := asSlice(mapValue(m, "IpPermissions", "ipPermissions"))
	for _, perm := range perms {
		pm := asMap(perm)
		if pm == nil {
			continue
		}
		if !permHasWorldCIDR(pm) {
			continue
		}
		proto := strings.TrimSpace(strings.ToLower(asString(mapValue(pm, "IpProtocol", "ipProtocol"))))
		if proto == "-1" || proto == "all" {
			critical = true
			continue
		}
		from, fromOK := asInt(mapValue(pm, "FromPort", "fromPort"))
		to, toOK := asInt(mapValue(pm, "ToPort", "toPort"))
		if !fromOK || !toOK {
			continue
		}
		if portInRange(22, from, to) || portInRange(3389, from, to) {
			high = true
		}
	}
	return critical, high
}

func permHasWorldCIDR(pm map[string]any) bool {
	for _, v := range asSlice(mapValue(pm, "IpRanges", "ipRanges")) {
		rm := asMap(v)
		if rm == nil {
			continue
		}
		if strings.TrimSpace(asString(mapValue(rm, "CidrIp", "cidrIp"))) == "0.0.0.0/0" {
			return true
		}
	}
	for _, v := range asSlice(mapValue(pm, "Ipv6Ranges", "ipv6Ranges")) {
		rm := asMap(v)
		if rm == nil {
			continue
		}
		if strings.TrimSpace(asString(mapValue(rm, "CidrIpv6", "cidrIpv6"))) == "::/0" {
			return true
		}
	}
	return false
}

func portInRange(port, from, to int) bool {
	if from > to {
		from, to = to, from
	}
	return port >= from && port <= to
}
