package cloudfront

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkcloudfront "github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newCloudFront func(cfg awsSDK.Config) cloudFrontAPI
}

func New() *Provider {
	return &Provider{newCloudFront: func(cfg awsSDK.Config) cloudFrontAPI { return sdkcloudfront.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "cloudfront" }
func (p *Provider) DisplayName() string { return "CloudFront" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeGlobal
}

type cloudFrontAPI interface {
	ListDistributions(ctx context.Context, params *sdkcloudfront.ListDistributionsInput, optFns ...func(*sdkcloudfront.Options)) (*sdkcloudfront.ListDistributionsOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("cloudfront provider requires account identity")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	return p.listGlobal(ctx, p.newCloudFront(cfg), req.Partition, req.AccountID)
}

func (p *Provider) listGlobal(ctx context.Context, api cloudFrontAPI, partition, accountID string) (providers.ListResult, error) {
	now := time.Now().UTC()
	var res providers.ListResult
	var marker *string
	for {
		out, err := api.ListDistributions(ctx, &sdkcloudfront.ListDistributionsInput{Marker: marker})
		if err != nil {
			return providers.ListResult{}, err
		}
		if out.DistributionList == nil {
			break
		}
		for _, d := range out.DistributionList.Items {
			n, stubs, edges := normalizeDistribution(partition, accountID, d, now)
			res.Nodes = append(res.Nodes, n)
			res.Nodes = append(res.Nodes, stubs...)
			res.Edges = append(res.Edges, edges...)
		}
		if out.DistributionList.IsTruncated == nil || !*out.DistributionList.IsTruncated || out.DistributionList.NextMarker == nil || strings.TrimSpace(*out.DistributionList.NextMarker) == "" {
			break
		}
		marker = out.DistributionList.NextMarker
	}
	return res, nil
}

func normalizeDistribution(partition, accountID string, d types.DistributionSummary, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	arn := strings.TrimSpace(awsToString(d.ARN))
	id := strings.TrimSpace(awsToString(d.Id))
	key := graph.EncodeResourceKey(partition, accountID, "global", "cloudfront:distribution", firstNonEmpty(arn, id))
	attrs := map[string]any{
		"status":      strings.TrimSpace(awsToString(d.Status)),
		"enabled":     awsToBool(d.Enabled),
		"domain":      strings.TrimSpace(awsToString(d.DomainName)),
		"priceClass":  strings.TrimSpace(string(d.PriceClass)),
		"httpVersion": strings.TrimSpace(string(d.HttpVersion)),
	}
	if d.LastModifiedTime != nil {
		attrs["updated_at"] = d.LastModifiedTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(id, arn),
		Service:     "cloudfront",
		Type:        "cloudfront:distribution",
		Arn:         arn,
		PrimaryID:   firstNonEmpty(arn, id),
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "cloudfront",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if waf := strings.TrimSpace(awsToString(d.WebACLId)); waf != "" {
		wafKey := graph.EncodeResourceKey(partition, accountID, "global", "wafv2:web-acl", waf)
		stubs = append(stubs, stubNode(wafKey, "wafv2", "wafv2:web-acl", shortArn(waf), now, "cloudfront"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: wafKey, Kind: "attached-to", Meta: map[string]any{"direct": true, "source": "cloudfront.waf"}, CollectedAt: now})
	}
	if d.ViewerCertificate != nil {
		if cert := strings.TrimSpace(awsToString(d.ViewerCertificate.ACMCertificateArn)); cert != "" {
			certKey := graph.EncodeResourceKey(partition, accountID, arnRegion(cert, "us-east-1"), "acm:certificate", cert)
			stubs = append(stubs, stubNode(certKey, "acm", "acm:certificate", shortArn(cert), now, "cloudfront"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: certKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "cloudfront.viewer-cert"}, CollectedAt: now})
		}
	}
	for _, origin := range d.Origins.Items {
		domain := strings.ToLower(strings.TrimSpace(awsToString(origin.DomainName)))
		if domain == "" {
			continue
		}
		if bucket, ok := s3BucketFromOriginDomain(domain); ok {
			toKey := graph.EncodeResourceKey(partition, accountID, "global", "s3:bucket", bucket)
			stubs = append(stubs, stubNode(toKey, "s3", "s3:bucket", bucket, now, "cloudfront"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "cloudfront.origin.s3"}, CollectedAt: now})
			continue
		}
		if strings.Contains(domain, ".elb.amazonaws.com") {
			lbRegion := regionFromELBDomain(domain)
			if lbRegion == "" {
				lbRegion = "global"
			}
			toKey := graph.EncodeResourceKey(partition, accountID, lbRegion, "elbv2:load-balancer", domain)
			stubs = append(stubs, stubNode(toKey, "elbv2", "elbv2:load-balancer", domain, now, "cloudfront"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "cloudfront.origin.elb"}, CollectedAt: now})
			continue
		}
		if apiID, apiRegion, ok := parseExecuteAPIDomain(domain); ok {
			toKey := graph.EncodeResourceKey(partition, accountID, apiRegion, "apigateway:rest-api", apiID)
			stubs = append(stubs, stubNode(toKey, "apigateway", "apigateway:rest-api", apiID, now, "cloudfront"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "cloudfront.origin.apigateway"}, CollectedAt: now})
		}
	}

	return node, stubs, edges
}

func s3BucketFromOriginDomain(domain string) (string, bool) {
	domain = strings.TrimSpace(strings.ToLower(domain))
	if domain == "" {
		return "", false
	}
	if i := strings.Index(domain, ".s3"); i > 0 {
		bucket := strings.TrimSpace(domain[:i])
		if bucket != "" {
			return bucket, true
		}
	}
	return "", false
}

func stubNode(key graph.ResourceKey, service, typ, display string, now time.Time, source string) graph.ResourceNode {
	_, _, _, _, primaryID, err := graph.ParseResourceKey(key)
	if err != nil {
		primaryID = ""
	}
	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     service,
		Type:        typ,
		PrimaryID:   primaryID,
		Tags:        map[string]string{},
		Attributes:  map[string]any{"stub": true},
		Raw:         []byte(`{}`),
		CollectedAt: now,
		Source:      source,
	}
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func awsToBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
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

func shortArn(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" {
		return ""
	}
	if i := strings.LastIndex(arn, "/"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	if i := strings.LastIndex(arn, ":"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	return arn
}

func arnRegion(arn, fallback string) string {
	parts := strings.SplitN(strings.TrimSpace(arn), ":", 6)
	if len(parts) < 6 || strings.TrimSpace(parts[3]) == "" {
		return fallback
	}
	return strings.TrimSpace(parts[3])
}

func parseExecuteAPIDomain(domain string) (apiID, region string, ok bool) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	parts := strings.Split(domain, ".")
	// <api-id>.execute-api.<region>.amazonaws.com
	if len(parts) < 5 {
		return "", "", false
	}
	if parts[1] != "execute-api" {
		return "", "", false
	}
	if parts[0] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func regionFromELBDomain(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimSuffix(domain, ".elb.amazonaws.com")
	if domain == "" {
		return ""
	}
	parts := strings.Split(domain, ".")
	if len(parts) == 0 {
		return ""
	}
	region := strings.TrimSpace(parts[len(parts)-1])
	if region == "" || !strings.Contains(region, "-") {
		return ""
	}
	return region
}
