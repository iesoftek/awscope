package lambda

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
	sdklambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newLambda func(cfg awsSDK.Config) lambdaAPI
}

func New() *Provider {
	return &Provider{newLambda: func(cfg awsSDK.Config) lambdaAPI { return sdklambda.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "lambda" }
func (p *Provider) DisplayName() string { return "Lambda" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type lambdaAPI interface {
	ListFunctions(ctx context.Context, params *sdklambda.ListFunctionsInput, optFns ...func(*sdklambda.Options)) (*sdklambda.ListFunctionsOutput, error)
	ListEventSourceMappings(ctx context.Context, params *sdklambda.ListEventSourceMappingsInput, optFns ...func(*sdklambda.Options)) (*sdklambda.ListEventSourceMappingsOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("lambda provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("lambda provider requires account identity")
	}
	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newLambda(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api lambdaAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()

	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge

	// Functions.
	var marker *string
	for {
		resp, err := api.ListFunctions(ctx, &sdklambda.ListFunctionsInput{Marker: marker})
		if err != nil {
			return nil, nil, err
		}
		for _, f := range resp.Functions {
			fn, stubs, es := normalizeFunction(partition, accountID, region, f, now)
			nodes = append(nodes, fn)
			nodes = append(nodes, stubs...)
			edges = append(edges, es...)
		}
		if resp.NextMarker == nil || *resp.NextMarker == "" {
			break
		}
		marker = resp.NextMarker
	}

	// Event source mappings: connect functions to sources (SQS, DynamoDB streams).
	var esmMarker *string
	for {
		resp, err := api.ListEventSourceMappings(ctx, &sdklambda.ListEventSourceMappingsInput{Marker: esmMarker})
		if err != nil {
			return nil, nil, err
		}
		for _, m := range resp.EventSourceMappings {
			fnArn := awsToString(m.FunctionArn)
			srcArn := awsToString(m.EventSourceArn)
			if fnArn == "" || srcArn == "" {
				continue
			}
			fnKey := graph.EncodeResourceKey(partition, accountID, region, "lambda:function", fnArn)

			if toKey, ok := eventSourceArnToKey(partition, accountID, region, srcArn); ok {
				edges = append(edges, graph.RelationshipEdge{
					From:        fnKey,
					To:          toKey,
					Kind:        "uses",
					Meta:        map[string]any{"direct": true, "source": "lambda.event-source-mapping"},
					CollectedAt: now,
				})
			}
		}
		if resp.NextMarker == nil || *resp.NextMarker == "" {
			break
		}
		esmMarker = resp.NextMarker
	}

	return nodes, edges, nil
}

func normalizeFunction(partition, accountID, region string, f types.FunctionConfiguration, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	arn := awsToString(f.FunctionArn)
	name := awsToString(f.FunctionName)
	if name == "" {
		name = arn
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "lambda:function", arn)
	raw, _ := json.Marshal(f)

	attrs := map[string]any{
		"runtime":      string(f.Runtime),
		"lastModified": awsToString(f.LastModified),
		"state":        string(f.State),
		"status":       string(f.StateReasonCode),
		"memorySize":   f.MemorySize,
		"timeout":      f.Timeout,
		"packageType":  string(f.PackageType),
	}

	node := graph.ResourceNode{
		Key:         key,
		DisplayName: name,
		Service:     "lambda",
		Type:        "lambda:function",
		Arn:         arn,
		PrimaryID:   arn,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "lambda",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge

	// function -> execution role
	if roleArn := awsToString(f.Role); roleArn != "" {
		roleKey := graph.EncodeResourceKey(partition, accountID, "global", "iam:role", roleArn)
		stubs = append(stubs, stubNode(roleKey, "iam", "iam:role", shortArn(roleArn), now, "lambda"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: roleKey, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}

	// function -> vpc/subnets/sgs
	if f.VpcConfig != nil {
		if vpcID := awsToString(f.VpcConfig.VpcId); vpcID != "" {
			vpcKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)
			stubs = append(stubs, stubNode(vpcKey, "ec2", "ec2:vpc", vpcID, now, "lambda"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: vpcKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
		for _, sn := range f.VpcConfig.SubnetIds {
			if sn == "" {
				continue
			}
			subnetKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", sn)
			stubs = append(stubs, stubNode(subnetKey, "ec2", "ec2:subnet", sn, now, "lambda"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: subnetKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
		for _, sg := range f.VpcConfig.SecurityGroupIds {
			if sg == "" {
				continue
			}
			sgKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sg)
			stubs = append(stubs, stubNode(sgKey, "ec2", "ec2:security-group", sg, now, "lambda"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: sgKey, Kind: "attached-to", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
	}

	return node, stubs, edges
}

func eventSourceArnToKey(partition, accountID, fallbackRegion, arn string) (graph.ResourceKey, bool) {
	// arn:partition:service:region:account:resource...
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return "", false
	}
	svc := parts[2]
	r := parts[3]
	if r == "" {
		r = fallbackRegion
	}
	switch svc {
	case "sqs":
		return graph.EncodeResourceKey(partition, accountID, r, "sqs:queue", arn), true
	case "dynamodb":
		// Table stream ARN -> table ARN is prefix before "/stream/".
		if i := strings.Index(arn, "/stream/"); i > 0 {
			tableArn := arn[:i]
			return graph.EncodeResourceKey(partition, accountID, r, "dynamodb:table", tableArn), true
		}
		return "", false
	default:
		return "", false
	}
}

func shortArn(arn string) string {
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
		Arn:         "",
		PrimaryID:   primaryID,
		Tags:        map[string]string{},
		Attributes:  map[string]any{},
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
