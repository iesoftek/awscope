package apigateway

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
	sdkapigateway "github.com/aws/aws-sdk-go-v2/service/apigateway"
	"github.com/aws/aws-sdk-go-v2/service/apigateway/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newAPIGateway func(cfg awsSDK.Config) apiGatewayAPI
}

func New() *Provider {
	return &Provider{newAPIGateway: func(cfg awsSDK.Config) apiGatewayAPI { return sdkapigateway.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "apigateway" }
func (p *Provider) DisplayName() string { return "API Gateway" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type apiGatewayAPI interface {
	GetRestApis(ctx context.Context, params *sdkapigateway.GetRestApisInput, optFns ...func(*sdkapigateway.Options)) (*sdkapigateway.GetRestApisOutput, error)
	GetDomainNames(ctx context.Context, params *sdkapigateway.GetDomainNamesInput, optFns ...func(*sdkapigateway.Options)) (*sdkapigateway.GetDomainNamesOutput, error)
	GetBasePathMappings(ctx context.Context, params *sdkapigateway.GetBasePathMappingsInput, optFns ...func(*sdkapigateway.Options)) (*sdkapigateway.GetBasePathMappingsOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("apigateway provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("apigateway provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newAPIGateway(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api apiGatewayAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge
	var position *string
	for {
		out, err := api.GetRestApis(ctx, &sdkapigateway.GetRestApisInput{Position: position})
		if err != nil {
			return nil, nil, err
		}
		for _, a := range out.Items {
			n, stubs, es := normalizeAPI(partition, accountID, region, a, now)
			nodes = append(nodes, n)
			nodes = append(nodes, stubs...)
			edges = append(edges, es...)
		}
		if out.Position == nil || strings.TrimSpace(*out.Position) == "" || (position != nil && *position == *out.Position) {
			break
		}
		position = out.Position
	}

	domainNodes, domainStubs, domainEdges, err := p.collectDomainNames(ctx, api, partition, accountID, region, now)
	if err != nil {
		return nil, nil, err
	}
	nodes = append(nodes, domainNodes...)
	nodes = append(nodes, domainStubs...)
	edges = append(edges, domainEdges...)
	return nodes, edges, nil
}

func (p *Provider) collectDomainNames(ctx context.Context, api apiGatewayAPI, partition, accountID, region string, now time.Time) ([]graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge, error) {
	var nodes []graph.ResourceNode
	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge

	var position *string
	for {
		out, err := api.GetDomainNames(ctx, &sdkapigateway.GetDomainNamesInput{Position: position})
		if err != nil {
			return nil, nil, nil, err
		}
		for _, d := range out.Items {
			n, domainStubs, domainEdges := normalizeDomainName(partition, accountID, region, d, now)
			nodes = append(nodes, n)
			stubs = append(stubs, domainStubs...)
			edges = append(edges, domainEdges...)

			domainName := strings.TrimSpace(awsToString(d.DomainName))
			if domainName == "" {
				continue
			}
			var bpPosition *string
			for {
				bpOut, err := api.GetBasePathMappings(ctx, &sdkapigateway.GetBasePathMappingsInput{
					DomainName: awsSDK.String(domainName),
					Position:   bpPosition,
				})
				if err != nil {
					return nil, nil, nil, err
				}
				for _, bpm := range bpOut.Items {
					baseStubs, baseEdges := normalizeBasePathMapping(partition, accountID, region, n.Key, bpm, now)
					stubs = append(stubs, baseStubs...)
					edges = append(edges, baseEdges...)
				}
				if bpOut.Position == nil || strings.TrimSpace(*bpOut.Position) == "" || (bpPosition != nil && *bpPosition == *bpOut.Position) {
					break
				}
				bpPosition = bpOut.Position
			}
		}
		if out.Position == nil || strings.TrimSpace(*out.Position) == "" || (position != nil && *position == *out.Position) {
			break
		}
		position = out.Position
	}
	return nodes, stubs, edges, nil
}

func normalizeAPI(partition, accountID, region string, a types.RestApi, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	id := strings.TrimSpace(awsToString(a.Id))
	name := strings.TrimSpace(awsToString(a.Name))
	arn := ""
	if id != "" {
		arn = fmt.Sprintf("arn:%s:apigateway:%s::/restapis/%s", partition, region, id)
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "apigateway:rest-api", firstNonEmpty(id, arn))
	attrs := map[string]any{
		"status":                 strings.TrimSpace(string(a.ApiStatus)),
		"apiKeySource":           strings.TrimSpace(string(a.ApiKeySource)),
		"disableExecuteEndpoint": a.DisableExecuteApiEndpoint,
		"endpointAccessMode":     strings.TrimSpace(string(a.EndpointAccessMode)),
		"minimumCompressionSize": a.MinimumCompressionSize,
		"description":            strings.TrimSpace(awsToString(a.Description)),
	}
	if a.CreatedDate != nil {
		attrs["created_at"] = a.CreatedDate.UTC().Format("2006-01-02 15:04")
	}
	if a.EndpointConfiguration != nil {
		var typesVals []string
		for _, t := range a.EndpointConfiguration.Types {
			typesVals = append(typesVals, string(t))
		}
		attrs["endpointTypes"] = typesVals
		attrs["ipAddressType"] = string(a.EndpointConfiguration.IpAddressType)
	}
	raw, _ := json.Marshal(a)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(name, id),
		Service:     "apigateway",
		Type:        "apigateway:rest-api",
		Arn:         arn,
		PrimaryID:   firstNonEmpty(id, arn),
		Tags:        a.Tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "apigateway",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if a.EndpointConfiguration != nil {
		for _, vpce := range a.EndpointConfiguration.VpcEndpointIds {
			vpce = strings.TrimSpace(vpce)
			if vpce == "" {
				continue
			}
			toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc-endpoint", vpce)
			stubs = append(stubs, stubNode(toKey, "ec2", "ec2:vpc-endpoint", vpce, now, "apigateway"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "apigateway.vpce"}, CollectedAt: now})
		}
	}

	return node, stubs, edges
}

func normalizeDomainName(partition, accountID, region string, d types.DomainName, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	domain := strings.TrimSpace(awsToString(d.DomainName))
	arn := strings.TrimSpace(awsToString(d.DomainNameArn))
	if arn == "" && domain != "" {
		arn = fmt.Sprintf("arn:%s:apigateway:%s::/domainnames/%s", partition, region, domain)
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "apigateway:domain-name", firstNonEmpty(arn, domain))

	attrs := map[string]any{
		"status":               strings.TrimSpace(string(d.DomainNameStatus)),
		"statusMessage":        strings.TrimSpace(awsToString(d.DomainNameStatusMessage)),
		"securityPolicy":       strings.TrimSpace(string(d.SecurityPolicy)),
		"routingMode":          strings.TrimSpace(string(d.RoutingMode)),
		"endpointAccessMode":   strings.TrimSpace(string(d.EndpointAccessMode)),
		"regionalDomainName":   strings.TrimSpace(awsToString(d.RegionalDomainName)),
		"distributionDomain":   strings.TrimSpace(awsToString(d.DistributionDomainName)),
		"regionalHostedZoneId": strings.TrimSpace(awsToString(d.RegionalHostedZoneId)),
	}
	if d.CertificateUploadDate != nil {
		attrs["certificateUploadedAt"] = d.CertificateUploadDate.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(domain, arn),
		Service:     "apigateway",
		Type:        "apigateway:domain-name",
		Arn:         arn,
		PrimaryID:   firstNonEmpty(arn, domain),
		Tags:        d.Tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "apigateway",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	certArn := strings.TrimSpace(awsToString(d.RegionalCertificateArn))
	if certArn == "" {
		certArn = strings.TrimSpace(awsToString(d.CertificateArn))
	}
	if certArn != "" {
		certKey := graph.EncodeResourceKey(partition, accountID, arnRegion(certArn, region), "acm:certificate", certArn)
		stubs = append(stubs, stubNode(certKey, "acm", "acm:certificate", shortArn(certArn), now, "apigateway"))
		edges = append(edges, graph.RelationshipEdge{
			From:        key,
			To:          certKey,
			Kind:        "uses",
			Meta:        map[string]any{"direct": true, "source": "apigateway.domain.cert"},
			CollectedAt: now,
		})
	}
	if cfDomain := strings.TrimSpace(awsToString(d.DistributionDomainName)); cfDomain != "" {
		cfKey := graph.EncodeResourceKey(partition, accountID, "global", "cloudfront:distribution", cfDomain)
		stubs = append(stubs, stubNode(cfKey, "cloudfront", "cloudfront:distribution", cfDomain, now, "apigateway"))
		edges = append(edges, graph.RelationshipEdge{
			From:        key,
			To:          cfKey,
			Kind:        "uses",
			Meta:        map[string]any{"direct": true, "source": "apigateway.domain.cloudfront"},
			CollectedAt: now,
		})
	}
	return node, stubs, edges
}

func normalizeBasePathMapping(partition, accountID, region string, domainKey graph.ResourceKey, bpm types.BasePathMapping, now time.Time) ([]graph.ResourceNode, []graph.RelationshipEdge) {
	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge

	restAPIID := strings.TrimSpace(awsToString(bpm.RestApiId))
	if restAPIID == "" {
		return stubs, edges
	}
	toKey := graph.EncodeResourceKey(partition, accountID, region, "apigateway:rest-api", restAPIID)
	stubs = append(stubs, stubNode(toKey, "apigateway", "apigateway:rest-api", restAPIID, now, "apigateway"))
	edges = append(edges, graph.RelationshipEdge{
		From: domainKey,
		To:   toKey,
		Kind: "targets",
		Meta: map[string]any{
			"direct":   true,
			"source":   "apigateway.base-path-mapping",
			"basePath": strings.TrimSpace(awsToString(bpm.BasePath)),
			"stage":    strings.TrimSpace(awsToString(bpm.Stage)),
		},
		CollectedAt: now,
	})
	return stubs, edges
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

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}

func arnRegion(arn, fallback string) string {
	parts := strings.SplitN(strings.TrimSpace(arn), ":", 6)
	if len(parts) < 6 || strings.TrimSpace(parts[3]) == "" {
		return fallback
	}
	return strings.TrimSpace(parts[3])
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
