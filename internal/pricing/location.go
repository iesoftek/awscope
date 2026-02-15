package pricing

import "strings"

// RegionToLocation returns the AWS Pricing "location" string for a region code.
// This is a static mapping; if we don't recognize the region, pricing lookups
// will be treated as unknown.
func RegionToLocation(region string) (string, bool) {
	region = strings.TrimSpace(strings.ToLower(region))
	if region == "" || region == "global" {
		return "", false
	}
	loc, ok := regionLocations[region]
	return loc, ok
}

// RegionToUsagePrefix returns the region usage prefix commonly found in Pricing "usagetype"
// fields and CUR/CE line items (for example "USE2" for us-east-2).
//
// This mapping is not identical to RegionToLocation and is used for lookups that need an
// exact "usagetype" match (such as Fargate vCPU/GB-hours and ELB LoadBalancerUsage).
//
// If we don't recognize the region, callers should treat the pricing as unknown.
func RegionToUsagePrefix(region string) (string, bool) {
	region = strings.TrimSpace(strings.ToLower(region))
	if region == "" || region == "global" {
		return "", false
	}
	p, ok := regionUsagePrefix[region]
	return p, ok
}

// Source: common AWS Pricing "location" strings. This does not aim to be exhaustive for all partitions.
var regionLocations = map[string]string{
	"us-east-1":      "US East (N. Virginia)",
	"us-east-2":      "US East (Ohio)",
	"us-west-1":      "US West (N. California)",
	"us-west-2":      "US West (Oregon)",
	"ca-central-1":   "Canada (Central)",
	"sa-east-1":      "South America (Sao Paulo)",
	"eu-north-1":     "EU (Stockholm)",
	"eu-west-1":      "EU (Ireland)",
	"eu-west-2":      "EU (London)",
	"eu-west-3":      "EU (Paris)",
	"eu-central-1":   "EU (Frankfurt)",
	"eu-central-2":   "EU (Zurich)",
	"eu-south-1":     "EU (Milan)",
	"eu-south-2":     "EU (Spain)",
	"me-south-1":     "Middle East (Bahrain)",
	"me-central-1":   "Middle East (UAE)",
	"il-central-1":   "Israel (Tel Aviv)",
	"af-south-1":     "Africa (Cape Town)",
	"ap-south-1":     "Asia Pacific (Mumbai)",
	"ap-south-2":     "Asia Pacific (Hyderabad)",
	"ap-southeast-1": "Asia Pacific (Singapore)",
	"ap-southeast-2": "Asia Pacific (Sydney)",
	"ap-southeast-3": "Asia Pacific (Jakarta)",
	"ap-southeast-4": "Asia Pacific (Melbourne)",
	"ap-northeast-1": "Asia Pacific (Tokyo)",
	"ap-northeast-2": "Asia Pacific (Seoul)",
	"ap-northeast-3": "Asia Pacific (Osaka)",
	"ap-east-1":      "Asia Pacific (Hong Kong)",
	"cn-north-1":     "China (Beijing)",
	"cn-northwest-1": "China (Ningxia)",
}

// Source: AWS billing/CUR/CE region usage prefixes (commercial regions only).
// These prefixes appear in various "usagetype" fields returned by the Pricing API.
var regionUsagePrefix = map[string]string{
	"us-east-1": "USE1",
	"us-east-2": "USE2",
	"us-west-1": "USW1",
	"us-west-2": "USW2",

	"ca-central-1": "CAN1",
	"sa-east-1":    "SAE1",

	"eu-north-1":   "EUN1",
	"eu-west-1":    "EU",
	"eu-west-2":    "EUW2",
	"eu-west-3":    "EUW3",
	"eu-central-1": "EUC1",
	"eu-central-2": "EUC2",
	"eu-south-1":   "EUS1",
	"eu-south-2":   "EUS2",

	"me-south-1":   "MES1",
	"me-central-1": "MEC1",
	"il-central-1": "ILC1",
	"af-south-1":   "AFS1",

	"ap-south-1":     "APS3",
	"ap-south-2":     "APS5",
	"ap-southeast-1": "APS1",
	"ap-southeast-2": "APS2",
	"ap-southeast-3": "APS6",
	"ap-southeast-4": "APS7",
	"ap-northeast-1": "APN1",
	"ap-northeast-2": "APN2",
	"ap-northeast-3": "APN3",
	"ap-east-1":      "APE1",
}
