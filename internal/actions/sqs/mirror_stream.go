package sqs

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"awscope/internal/actions"
	"awscope/internal/actions/registry"
	"awscope/internal/graph"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdksns "github.com/aws/aws-sdk-go-v2/service/sns"
	sdksqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
)

func init() {
	registry.Register(MirrorStream{})
}

var (
	newSQSClient = func(cfg awsSDK.Config) sqsAPI { return sdksqs.NewFromConfig(cfg) }
	newSNSClient = func(cfg awsSDK.Config) snsAPI { return sdksns.NewFromConfig(cfg) }
)

type MirrorStream struct{}

func (MirrorStream) ID() string    { return "sqs.mirror-stream" }
func (MirrorStream) Title() string { return "Stream messages (SNS mirror, ephemeral)" }
func (MirrorStream) PreferEmbeddedTUI() bool {
	return true
}
func (MirrorStream) Description() string {
	return "Create/update an ephemeral SNS mirror queue, stream mirrored messages, and tear it down on exit"
}
func (MirrorStream) Risk() actions.RiskLevel { return actions.RiskMedium }

func (MirrorStream) Applicable(node graph.ResourceNode) bool {
	if node.Service != "sqs" || node.Type != "sqs:queue" {
		return false
	}
	if strings.TrimSpace(queueURLFromNode(node)) != "" {
		return true
	}
	if strings.TrimSpace(node.Arn) != "" {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(node.PrimaryID), "arn:") {
		return true
	}
	return strings.TrimSpace(node.DisplayName) != ""
}

func (a MirrorStream) Execute(ctx context.Context, execCtx actions.ExecContext, node graph.ResourceNode) (actions.Result, error) {
	return a.ExecuteTerminal(ctx, execCtx, node)
}

func (MirrorStream) ExecuteTerminal(ctx context.Context, execCtx actions.ExecContext, node graph.ResourceNode) (res actions.Result, err error) {
	if err := requireRegional(execCtx.Region); err != nil {
		return actions.Result{}, err
	}

	in := nonNilReader(execCtx.Stdin, os.Stdin)
	out := nonNilWriter(execCtx.Stdout, os.Stdout)
	errOut := nonNilWriter(execCtx.Stderr, os.Stderr)
	prompt := newPrompter(in, out)

	sqsCli := newSQSClient(execCtx.AWSConfig)
	snsCli := newSNSClient(execCtx.AWSConfig)

	source, err := resolveSourceQueue(ctx, sqsCli, node)
	if err != nil {
		return actions.Result{}, err
	}
	topicARNs, err := discoverSourceTopics(ctx, snsCli, source.arn)
	if err != nil {
		return actions.Result{}, err
	}
	if len(topicARNs) == 0 {
		return actions.Result{}, fmt.Errorf("queue %s has no SNS fanout subscriptions; mirror mode requires SNS -> SQS topology", source.name)
	}

	fmt.Fprintf(out, "source queue: %s (%s)\n", source.name, source.arn)
	fmt.Fprintf(out, "discovered %d SNS topic(s): %s\n", len(topicARNs), strings.Join(shortTopicList(topicARNs), ", "))

	ok, err := prompt.confirmStep(2, 7, fmt.Sprintf("continue with mirror setup for %d topic(s)", len(topicARNs)))
	if err != nil {
		return actions.Result{}, err
	}
	if !ok {
		return actions.Result{}, fmt.Errorf("aborted by user at discovery confirmation")
	}

	state := sessionState{
		sourceQueueARN: source.arn,
		topicARNs:      append([]string(nil), topicARNs...),
	}

	defer func() {
		if !state.needsTeardown() {
			return
		}
		tdCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		autoApprove := execCtx.AutoApproveTeardownOnCancel && ctx.Err() != nil
		sum, tdErr := teardownSession(tdCtx, prompt, sqsCli, snsCli, execCtx, &state, out, errOut, autoApprove)
		if tdErr != nil {
			if err == nil {
				err = tdErr
			} else {
				fmt.Fprintf(errOut, "teardown error: %v\n", tdErr)
			}
		}
		if res.Data == nil {
			res.Data = map[string]any{}
		}
		res.Data["teardown"] = sum.toMap()
	}()

	mirrorName := mirrorQueueName(source.name, source.arn, source.fifo)
	state.mirrorQueueName = mirrorName
	mirrorURL, exists, err := getQueueURLByName(ctx, sqsCli, mirrorName)
	if err != nil {
		return actions.Result{}, err
	}
	if !exists {
		ok, err := prompt.confirmStep(3, 7, fmt.Sprintf("create mirror queue %s", mirrorName))
		if err != nil {
			return actions.Result{}, err
		}
		if !ok {
			return actions.Result{}, fmt.Errorf("aborted by user before mirror queue creation")
		}
		mirrorURL, err = createMirrorQueue(ctx, sqsCli, mirrorName, source)
		if err != nil {
			return actions.Result{}, fmt.Errorf("create mirror queue: %w", err)
		}
		state.mirrorQueueCreated = true
		state.mirrorQueueDeleteOnExit = true
	} else {
		owned, ownErr := isOwnedActionQueue(ctx, sqsCli, mirrorURL, map[string]string{
			"awscope:mirror_for": source.arn,
		})
		if ownErr != nil {
			fmt.Fprintf(errOut, "warning: could not inspect queue ownership tags for %s: %v\n", mirrorName, ownErr)
		}
		state.mirrorQueueDeleteOnExit = owned
	}
	state.mirrorQueueURL = mirrorURL

	mirrorARN, policyDoc, err := getQueueARNAndPolicy(ctx, sqsCli, mirrorURL)
	if err != nil {
		return actions.Result{}, err
	}
	state.mirrorQueueARN = mirrorARN

	ok, err = prompt.confirmStep(4, 7, fmt.Sprintf("apply/patch mirror queue policy for %d topic(s)", len(topicARNs)))
	if err != nil {
		return actions.Result{}, err
	}
	if !ok {
		return actions.Result{}, fmt.Errorf("aborted by user before policy update")
	}
	patched, err := ensureMirrorPolicy(ctx, sqsCli, mirrorURL, mirrorARN, policyDoc, topicARNs)
	if err != nil {
		return actions.Result{}, err
	}
	state.policyPatched = patched

	missingTopics, err := findMissingMirrorSubscriptions(ctx, snsCli, topicARNs, mirrorARN)
	if err != nil {
		return actions.Result{}, err
	}
	ok, err = prompt.confirmStep(5, 7, fmt.Sprintf("create %d missing mirror subscription(s)", len(missingTopics)))
	if err != nil {
		return actions.Result{}, err
	}
	if !ok {
		return actions.Result{}, fmt.Errorf("aborted by user before subscription setup")
	}
	createdSubs, err := subscribeMirrorTopics(ctx, snsCli, missingTopics, mirrorARN)
	if err != nil {
		return actions.Result{}, err
	}
	state.createdSubscriptionARNs = append(state.createdSubscriptionARNs, createdSubs...)

	ok, err = prompt.confirmStep(6, 7, fmt.Sprintf("start streaming from mirror queue %s", mirrorName))
	if err != nil {
		return actions.Result{}, err
	}
	if !ok {
		return actions.Result{}, fmt.Errorf("aborted by user before starting stream")
	}

	fmt.Fprintf(out, "streaming from mirror queue %s (%s). Press Ctrl+C to stop.\n", mirrorName, mirrorURL)
	streamCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	stats, err := streamMirrorMessages(streamCtx, sqsCli, mirrorURL, out, errOut)
	if err != nil {
		return actions.Result{}, err
	}

	res = actions.Result{Status: "SUCCEEDED", Data: map[string]any{
		"mode":                        "sqs-sns-mirror-stream",
		"source_queue_arn":            source.arn,
		"source_queue_name":           source.name,
		"mirror_queue_name":           mirrorName,
		"mirror_queue_url":            mirrorURL,
		"mirror_queue_arn":            mirrorARN,
		"topics":                      topicARNs,
		"created_subscriptions":       len(createdSubs),
		"mirror_queue_created":        state.mirrorQueueCreated,
		"mirror_queue_delete_on_exit": state.mirrorQueueDeleteOnExit,
		"policy_patched":              patched,
		"messages_streamed":           stats.Streamed,
		"messages_deleted":            stats.Deleted,
		"receive_polls":               stats.Polls,
		"profile":                     execCtx.Profile,
		"region":                      execCtx.Region,
		"stream_exit":                 "graceful",
		"confirmation_mode":           "per-step",
		"primary_queue_read_block":    true,
	}}
	return res, nil
}

type sqsAPI interface {
	GetQueueAttributes(ctx context.Context, params *sdksqs.GetQueueAttributesInput, optFns ...func(*sdksqs.Options)) (*sdksqs.GetQueueAttributesOutput, error)
	GetQueueUrl(ctx context.Context, params *sdksqs.GetQueueUrlInput, optFns ...func(*sdksqs.Options)) (*sdksqs.GetQueueUrlOutput, error)
	CreateQueue(ctx context.Context, params *sdksqs.CreateQueueInput, optFns ...func(*sdksqs.Options)) (*sdksqs.CreateQueueOutput, error)
	ListQueueTags(ctx context.Context, params *sdksqs.ListQueueTagsInput, optFns ...func(*sdksqs.Options)) (*sdksqs.ListQueueTagsOutput, error)
	SetQueueAttributes(ctx context.Context, params *sdksqs.SetQueueAttributesInput, optFns ...func(*sdksqs.Options)) (*sdksqs.SetQueueAttributesOutput, error)
	ReceiveMessage(ctx context.Context, params *sdksqs.ReceiveMessageInput, optFns ...func(*sdksqs.Options)) (*sdksqs.ReceiveMessageOutput, error)
	DeleteMessageBatch(ctx context.Context, params *sdksqs.DeleteMessageBatchInput, optFns ...func(*sdksqs.Options)) (*sdksqs.DeleteMessageBatchOutput, error)
	DeleteQueue(ctx context.Context, params *sdksqs.DeleteQueueInput, optFns ...func(*sdksqs.Options)) (*sdksqs.DeleteQueueOutput, error)
}

type snsAPI interface {
	ListSubscriptions(ctx context.Context, params *sdksns.ListSubscriptionsInput, optFns ...func(*sdksns.Options)) (*sdksns.ListSubscriptionsOutput, error)
	ListSubscriptionsByTopic(ctx context.Context, params *sdksns.ListSubscriptionsByTopicInput, optFns ...func(*sdksns.Options)) (*sdksns.ListSubscriptionsByTopicOutput, error)
	Subscribe(ctx context.Context, params *sdksns.SubscribeInput, optFns ...func(*sdksns.Options)) (*sdksns.SubscribeOutput, error)
	Unsubscribe(ctx context.Context, params *sdksns.UnsubscribeInput, optFns ...func(*sdksns.Options)) (*sdksns.UnsubscribeOutput, error)
}

type queueInfo struct {
	url                 string
	arn                 string
	name                string
	fifo                bool
	contentBasedDedup   bool
	messageRetentionSec string
}

func resolveSourceQueue(ctx context.Context, sqsCli sqsAPI, node graph.ResourceNode) (queueInfo, error) {
	url := queueURLFromNode(node)
	name := ""
	arnHint := ""
	if strings.HasPrefix(strings.TrimSpace(node.Arn), "arn:") {
		arnHint = strings.TrimSpace(node.Arn)
	}
	if arnHint == "" && strings.HasPrefix(strings.TrimSpace(node.PrimaryID), "arn:") {
		arnHint = strings.TrimSpace(node.PrimaryID)
	}
	if arnHint != "" {
		name = queueNameFromARN(arnHint)
	}
	if name == "" {
		name = strings.TrimSpace(node.DisplayName)
	}
	if name == "" {
		name = strings.TrimSpace(node.PrimaryID)
	}

	if strings.TrimSpace(url) == "" {
		if name == "" {
			return queueInfo{}, fmt.Errorf("unable to resolve queue URL from resource attributes")
		}
		resolved, exists, err := getQueueURLByName(ctx, sqsCli, name)
		if err != nil {
			return queueInfo{}, err
		}
		if !exists {
			return queueInfo{}, fmt.Errorf("queue URL not found for queue %q", name)
		}
		url = resolved
	}

	attrsOut, err := sqsCli.GetQueueAttributes(ctx, &sdksqs.GetQueueAttributesInput{
		QueueUrl: &url,
		AttributeNames: []sqstypes.QueueAttributeName{
			sqstypes.QueueAttributeNameQueueArn,
			sqstypes.QueueAttributeNameFifoQueue,
			sqstypes.QueueAttributeNameContentBasedDeduplication,
			sqstypes.QueueAttributeNameMessageRetentionPeriod,
		},
	})
	if err != nil {
		return queueInfo{}, fmt.Errorf("get source queue attributes: %w", err)
	}
	arn := strings.TrimSpace(attrsOut.Attributes[string(sqstypes.QueueAttributeNameQueueArn)])
	if arn == "" {
		arn = arnHint
	}
	if arn == "" {
		return queueInfo{}, fmt.Errorf("source queue arn not available")
	}
	name = queueNameFromARN(arn)
	if name == "" {
		name = strings.TrimSpace(node.DisplayName)
	}
	if name == "" {
		name = strings.TrimSpace(node.PrimaryID)
	}
	fifo := strings.EqualFold(strings.TrimSpace(attrsOut.Attributes[string(sqstypes.QueueAttributeNameFifoQueue)]), "true") || strings.HasSuffix(name, ".fifo")

	return queueInfo{
		url:                 url,
		arn:                 arn,
		name:                name,
		fifo:                fifo,
		contentBasedDedup:   strings.EqualFold(strings.TrimSpace(attrsOut.Attributes[string(sqstypes.QueueAttributeNameContentBasedDeduplication)]), "true"),
		messageRetentionSec: strings.TrimSpace(attrsOut.Attributes[string(sqstypes.QueueAttributeNameMessageRetentionPeriod)]),
	}, nil
}

func discoverSourceTopics(ctx context.Context, snsCli snsAPI, sourceQueueARN string) ([]string, error) {
	set := map[string]struct{}{}
	var token *string
	for {
		out, err := snsCli.ListSubscriptions(ctx, &sdksns.ListSubscriptionsInput{NextToken: token})
		if err != nil {
			return nil, fmt.Errorf("list sns subscriptions: %w", err)
		}
		for _, sub := range out.Subscriptions {
			if !strings.EqualFold(strings.TrimSpace(awsSDK.ToString(sub.Protocol)), "sqs") {
				continue
			}
			if strings.TrimSpace(awsSDK.ToString(sub.Endpoint)) != sourceQueueARN {
				continue
			}
			topicARN := strings.TrimSpace(awsSDK.ToString(sub.TopicArn))
			if topicARN == "" {
				continue
			}
			set[topicARN] = struct{}{}
		}
		if out.NextToken == nil || strings.TrimSpace(awsSDK.ToString(out.NextToken)) == "" {
			break
		}
		token = out.NextToken
	}
	return sortedKeys(set), nil
}

func getQueueURLByName(ctx context.Context, sqsCli sqsAPI, queueName string) (string, bool, error) {
	q := strings.TrimSpace(queueName)
	if q == "" {
		return "", false, nil
	}
	out, err := sqsCli.GetQueueUrl(ctx, &sdksqs.GetQueueUrlInput{QueueName: &q})
	if err != nil {
		if isSQSErrorCode(err, "AWS.SimpleQueueService.NonExistentQueue", "QueueDoesNotExist") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get queue url for %s: %w", q, err)
	}
	url := strings.TrimSpace(awsSDK.ToString(out.QueueUrl))
	if url == "" {
		return "", false, nil
	}
	return url, true, nil
}

func createMirrorQueue(ctx context.Context, sqsCli sqsAPI, mirrorName string, source queueInfo) (string, error) {
	attrs := map[string]string{}
	if source.fifo {
		attrs[string(sqstypes.QueueAttributeNameFifoQueue)] = "true"
		if source.contentBasedDedup {
			attrs[string(sqstypes.QueueAttributeNameContentBasedDeduplication)] = "true"
		}
	}
	if source.messageRetentionSec != "" {
		attrs[string(sqstypes.QueueAttributeNameMessageRetentionPeriod)] = source.messageRetentionSec
	}
	in := &sdksqs.CreateQueueInput{
		QueueName:  awsSDK.String(mirrorName),
		Attributes: attrs,
		Tags: map[string]string{
			"awscope:managed_by":          "awscope",
			"awscope:mirror_for":          source.arn,
			"awscope:mirror_source_queue": source.name,
		},
	}
	out, err := sqsCli.CreateQueue(ctx, in)
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(awsSDK.ToString(out.QueueUrl))
	if url == "" {
		resolved, exists, err := getQueueURLByName(ctx, sqsCli, mirrorName)
		if err != nil {
			return "", err
		}
		if !exists {
			return "", fmt.Errorf("mirror queue created but url lookup failed")
		}
		url = resolved
	}
	return url, nil
}

func getQueueARNAndPolicy(ctx context.Context, sqsCli sqsAPI, queueURL string) (string, string, error) {
	q := strings.TrimSpace(queueURL)
	if q == "" {
		return "", "", fmt.Errorf("empty queue url")
	}
	out, err := sqsCli.GetQueueAttributes(ctx, &sdksqs.GetQueueAttributesInput{
		QueueUrl: &q,
		AttributeNames: []sqstypes.QueueAttributeName{
			sqstypes.QueueAttributeNameQueueArn,
			sqstypes.QueueAttributeNamePolicy,
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("get mirror queue attributes: %w", err)
	}
	arn := strings.TrimSpace(out.Attributes[string(sqstypes.QueueAttributeNameQueueArn)])
	if arn == "" {
		return "", "", fmt.Errorf("mirror queue arn not available")
	}
	policy := strings.TrimSpace(out.Attributes[string(sqstypes.QueueAttributeNamePolicy)])
	return arn, policy, nil
}

func ensureMirrorPolicy(ctx context.Context, sqsCli sqsAPI, queueURL, mirrorARN, existingPolicy string, topicARNs []string) (bool, error) {
	doc, err := parsePolicy(existingPolicy)
	if err != nil {
		return false, fmt.Errorf("parse mirror queue policy: %w", err)
	}
	stmtRaw, _ := doc["Statement"].([]any)
	statements := stmtRaw
	changed := false
	for _, topicARN := range topicARNs {
		if hasPolicyStatement(statements, mirrorARN, topicARN) {
			continue
		}
		statements = append(statements, map[string]any{
			"Sid":       "AwscopeMirrorAllow" + shortHash(topicARN),
			"Effect":    "Allow",
			"Principal": map[string]any{"Service": "sns.amazonaws.com"},
			"Action":    "sqs:SendMessage",
			"Resource":  mirrorARN,
			"Condition": map[string]any{"ArnEquals": map[string]any{"aws:SourceArn": topicARN}},
		})
		changed = true
	}
	if !changed {
		return false, nil
	}
	doc["Statement"] = statements
	if _, ok := doc["Version"]; !ok {
		doc["Version"] = "2012-10-17"
	}
	blob, err := json.Marshal(doc)
	if err != nil {
		return false, fmt.Errorf("marshal mirror queue policy: %w", err)
	}
	q := strings.TrimSpace(queueURL)
	_, err = sqsCli.SetQueueAttributes(ctx, &sdksqs.SetQueueAttributesInput{
		QueueUrl: &q,
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNamePolicy): string(blob),
		},
	})
	if err != nil {
		return false, fmt.Errorf("set mirror queue policy: %w", err)
	}
	return true, nil
}

func findMissingMirrorSubscriptions(ctx context.Context, snsCli snsAPI, topicARNs []string, mirrorARN string) ([]string, error) {
	missing := make([]string, 0, len(topicARNs))
	for _, topicARN := range topicARNs {
		exists, err := hasTopicSubscriptionEndpoint(ctx, snsCli, topicARN, mirrorARN)
		if err != nil {
			return nil, err
		}
		if !exists {
			missing = append(missing, topicARN)
		}
	}
	return missing, nil
}

func hasTopicSubscriptionEndpoint(ctx context.Context, snsCli snsAPI, topicARN, endpointARN string) (bool, error) {
	var token *string
	for {
		out, err := snsCli.ListSubscriptionsByTopic(ctx, &sdksns.ListSubscriptionsByTopicInput{TopicArn: &topicARN, NextToken: token})
		if err != nil {
			return false, fmt.Errorf("list subscriptions by topic %s: %w", topicARN, err)
		}
		for _, sub := range out.Subscriptions {
			if strings.EqualFold(strings.TrimSpace(awsSDK.ToString(sub.Protocol)), "sqs") && strings.TrimSpace(awsSDK.ToString(sub.Endpoint)) == endpointARN {
				return true, nil
			}
		}
		if out.NextToken == nil || strings.TrimSpace(awsSDK.ToString(out.NextToken)) == "" {
			break
		}
		token = out.NextToken
	}
	return false, nil
}

func subscribeMirrorTopics(ctx context.Context, snsCli snsAPI, topicARNs []string, mirrorARN string) ([]string, error) {
	created := make([]string, 0, len(topicARNs))
	for _, topicARN := range topicARNs {
		out, err := snsCli.Subscribe(ctx, &sdksns.SubscribeInput{
			TopicArn:              &topicARN,
			Protocol:              awsSDK.String("sqs"),
			Endpoint:              &mirrorARN,
			ReturnSubscriptionArn: true,
		})
		if err != nil {
			return created, fmt.Errorf("subscribe mirror queue to topic %s: %w", topicARN, err)
		}
		arn := strings.TrimSpace(awsSDK.ToString(out.SubscriptionArn))
		if strings.HasPrefix(arn, "arn:") {
			created = append(created, arn)
		}
	}
	return created, nil
}

type streamStats struct {
	Streamed int
	Deleted  int
	Polls    int
}

func streamMirrorMessages(ctx context.Context, sqsCli sqsAPI, queueURL string, out io.Writer, errOut io.Writer) (streamStats, error) {
	stats := streamStats{}
	for {
		select {
		case <-ctx.Done():
			return stats, nil
		default:
		}

		q := queueURL
		resp, err := sqsCli.ReceiveMessage(ctx, &sdksqs.ReceiveMessageInput{
			QueueUrl:            &q,
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     20,
			MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{
				sqstypes.MessageSystemAttributeNameSentTimestamp,
				sqstypes.MessageSystemAttributeNameApproximateReceiveCount,
				sqstypes.MessageSystemAttributeNameMessageGroupId,
				sqstypes.MessageSystemAttributeNameSequenceNumber,
			},
			MessageAttributeNames: []string{"All"},
		})
		if err != nil {
			if ctx.Err() != nil {
				return stats, nil
			}
			fmt.Fprintf(errOut, "stream receive error: %v\n", err)
			time.Sleep(2 * time.Second)
			continue
		}
		stats.Polls++
		if len(resp.Messages) == 0 {
			continue
		}

		delEntries := make([]sqstypes.DeleteMessageBatchRequestEntry, 0, len(resp.Messages))
		for i, msg := range resp.Messages {
			fmt.Fprintln(out, formatMessageLine(msg))
			stats.Streamed++
			receipt := strings.TrimSpace(awsSDK.ToString(msg.ReceiptHandle))
			if receipt == "" {
				continue
			}
			delEntries = append(delEntries, sqstypes.DeleteMessageBatchRequestEntry{
				Id:            awsSDK.String(fmt.Sprintf("m%d", i)),
				ReceiptHandle: &receipt,
			})
		}
		if len(delEntries) == 0 {
			continue
		}
		delOut, err := sqsCli.DeleteMessageBatch(ctx, &sdksqs.DeleteMessageBatchInput{
			QueueUrl: &q,
			Entries:  delEntries,
		})
		if err != nil {
			fmt.Fprintf(errOut, "stream delete error: %v\n", err)
			continue
		}
		stats.Deleted += len(delEntries) - len(delOut.Failed)
		if len(delOut.Failed) > 0 {
			for _, f := range delOut.Failed {
				fmt.Fprintf(errOut, "stream delete failed id=%s code=%s message=%s\n", awsSDK.ToString(f.Id), awsSDK.ToString(f.Code), awsSDK.ToString(f.Message))
			}
		}
	}
}

func formatMessageLine(msg sqstypes.Message) string {
	msgID := strings.TrimSpace(awsSDK.ToString(msg.MessageId))
	if msgID == "" {
		msgID = "-"
	}
	body := strings.TrimSpace(awsSDK.ToString(msg.Body))
	topic := ""
	if t, b, ok := parseSNSEnvelope(body); ok {
		topic = t
		if strings.TrimSpace(b) != "" {
			body = b
		}
	}
	payload := formatPayloadForDisplay(body)
	rc := strings.TrimSpace(msg.Attributes[string(sqstypes.MessageSystemAttributeNameApproximateReceiveCount)])
	if rc == "" {
		rc = "1"
	}
	ts := sentTimestamp(msg.Attributes[string(sqstypes.MessageSystemAttributeNameSentTimestamp)])
	parts := []string{fmt.Sprintf("[%s]", ts), fmt.Sprintf("id=%s", msgID), fmt.Sprintf("rc=%s", rc)}
	if topic != "" {
		parts = append(parts, fmt.Sprintf("topic=%s", queueNameFromARN(topic)))
	}
	if g := strings.TrimSpace(msg.Attributes[string(sqstypes.MessageSystemAttributeNameMessageGroupId)]); g != "" {
		parts = append(parts, "group="+g)
	}
	if s := strings.TrimSpace(msg.Attributes[string(sqstypes.MessageSystemAttributeNameSequenceNumber)]); s != "" {
		parts = append(parts, "seq="+s)
	}
	head := strings.Join(parts, " ")
	return strings.Join([]string{head, "msg:", payload, ""}, "\n")
}

func parseSNSEnvelope(body string) (topicARN string, payload string, ok bool) {
	var env struct {
		TopicArn string `json:"TopicArn"`
		Message  string `json:"Message"`
	}
	if !json.Valid([]byte(body)) {
		return "", "", false
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return "", "", false
	}
	if strings.TrimSpace(env.TopicArn) == "" {
		return "", "", false
	}
	return strings.TrimSpace(env.TopicArn), env.Message, true
}

func sentTimestamp(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	ms, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04:05")
}

func formatPayloadForDisplay(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	if !json.Valid([]byte(s)) {
		return s
	}
	var doc any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return s
	}
	doc = expandNestedJSONStrings(doc, 0)
	pretty, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return s
	}
	return string(pretty)
}

func isOwnedActionQueue(ctx context.Context, sqsCli sqsAPI, queueURL string, expectedTags map[string]string) (bool, error) {
	q := strings.TrimSpace(queueURL)
	if q == "" {
		return false, nil
	}
	out, err := sqsCli.ListQueueTags(ctx, &sdksqs.ListQueueTagsInput{QueueUrl: &q})
	if err != nil {
		return false, err
	}
	tags := out.Tags
	if !strings.EqualFold(strings.TrimSpace(tags["awscope:managed_by"]), "awscope") {
		return false, nil
	}
	for k, want := range expectedTags {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		if strings.TrimSpace(tags[k]) != want {
			return false, nil
		}
	}
	return true, nil
}

func expandNestedJSONStrings(v any, depth int) any {
	if depth >= 4 {
		return v
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = expandNestedJSONStrings(vv, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = expandNestedJSONStrings(x[i], depth+1)
		}
		return out
	case string:
		s := strings.TrimSpace(x)
		if s == "" || (!strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[")) {
			return x
		}
		if !json.Valid([]byte(s)) {
			return x
		}
		var nested any
		if err := json.Unmarshal([]byte(s), &nested); err != nil {
			return x
		}
		return expandNestedJSONStrings(nested, depth+1)
	default:
		return v
	}
}

func parsePolicy(in string) (map[string]any, error) {
	if strings.TrimSpace(in) == "" {
		return map[string]any{"Version": "2012-10-17", "Statement": []any{}}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(in), &doc); err != nil {
		return nil, err
	}
	if _, ok := doc["Statement"]; !ok {
		doc["Statement"] = []any{}
	}
	return doc, nil
}

func hasPolicyStatement(statements []any, mirrorARN, topicARN string) bool {
	for _, raw := range statements {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(asString(m["Action"])) != "sqs:SendMessage" {
			continue
		}
		if strings.TrimSpace(asString(m["Resource"])) != mirrorARN {
			continue
		}
		cond, _ := m["Condition"].(map[string]any)
		arnEq, _ := cond["ArnEquals"].(map[string]any)
		if strings.TrimSpace(asString(arnEq["aws:SourceArn"])) == topicARN {
			return true
		}
	}
	return false
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return ""
	}
}

func queueURLFromNode(node graph.ResourceNode) string {
	if node.Attributes == nil {
		return ""
	}
	if v, ok := node.Attributes["url"].(string); ok {
		return strings.TrimSpace(v)
	}
	if v, ok := node.Attributes["queueUrl"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func queueNameFromARN(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" {
		return ""
	}
	if i := strings.LastIndex(arn, ":"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	return arn
}

func mirrorQueueName(sourceName, sourceARN string, fifo bool) string {
	base := strings.TrimSpace(sourceName)
	if base == "" {
		base = queueNameFromARN(sourceARN)
	}
	base = strings.TrimSuffix(base, ".fifo")
	candidate := base + "-awscope-mirror"
	suffix := ""
	if fifo {
		suffix = ".fifo"
	}
	maxLen := 80 - len(suffix)
	if len(candidate) > maxLen {
		h := shortHash(sourceARN)
		keep := maxLen - len("-m-") - len(h)
		if keep < 1 {
			keep = 1
		}
		candidate = candidate[:keep] + "-m-" + h
	}
	return candidate + suffix
}

func shortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func shortTopicList(topicARNs []string) []string {
	out := make([]string, 0, len(topicARNs))
	for _, arn := range topicARNs {
		out = append(out, queueNameFromARN(arn))
	}
	return out
}

func isSQSErrorCode(err error, codes ...string) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	code := strings.TrimSpace(apiErr.ErrorCode())
	for _, c := range codes {
		if code == c {
			return true
		}
	}
	return false
}

func requireRegional(region string) error {
	r := strings.TrimSpace(region)
	if r == "" || r == "global" {
		return fmt.Errorf("action requires a regional resource, got region=%q", region)
	}
	return nil
}

type prompter struct {
	reader *bufio.Reader
	out    io.Writer
}

func newPrompter(in io.Reader, out io.Writer) *prompter {
	if in == nil {
		return &prompter{out: out}
	}
	return &prompter{reader: bufio.NewReader(in), out: out}
}

func (p *prompter) confirmStep(step, total int, desc string) (bool, error) {
	return p.confirm(fmt.Sprintf("[step %d/%d] %s. Proceed? [y/N]: ", step, total, desc))
}

func (p *prompter) confirmTeardown(step, total int, desc string) (bool, error) {
	return p.confirm(fmt.Sprintf("[teardown %d/%d] %s. Proceed? [y/N]: ", step, total, desc))
}

func (p *prompter) confirm(prompt string) (bool, error) {
	if p == nil || p.reader == nil {
		return false, fmt.Errorf("interactive confirmation required")
	}
	if _, err := fmt.Fprint(nonNilWriter(p.out, os.Stdout), prompt); err != nil {
		return false, err
	}
	line, err := p.reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			if strings.TrimSpace(line) == "" {
				return false, fmt.Errorf("interactive confirmation required")
			}
		} else {
			return false, fmt.Errorf("interactive confirmation required: %w", err)
		}
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes", nil
}

type sessionState struct {
	mirrorQueueCreated      bool
	mirrorQueueDeleteOnExit bool
	mirrorQueueURL          string
	mirrorQueueARN          string
	mirrorQueueName         string
	createdSubscriptionARNs []string
	policyPatched           bool
	topicARNs               []string
	sourceQueueARN          string
}

func (s sessionState) needsTeardown() bool {
	return s.mirrorQueueDeleteOnExit || len(s.createdSubscriptionARNs) > 0
}

type teardownSummary struct {
	Unsubscribed       int
	SkippedUnsubscribe int
	QueueDeleted       bool
	QueueDeleteSkipped bool
	Retained           []string
}

func (t teardownSummary) toMap() map[string]any {
	return map[string]any{
		"unsubscribed":         t.Unsubscribed,
		"skipped_unsubscribe":  t.SkippedUnsubscribe,
		"queue_deleted":        t.QueueDeleted,
		"queue_delete_skipped": t.QueueDeleteSkipped,
		"retained":             t.Retained,
	}
}

func teardownSession(ctx context.Context, prompt *prompter, sqsCli sqsAPI, snsCli snsAPI, execCtx actions.ExecContext, state *sessionState, out io.Writer, errOut io.Writer, autoApprove bool) (teardownSummary, error) {
	summary := teardownSummary{}
	total := 0
	if len(state.createdSubscriptionARNs) > 0 {
		total++
	}
	if state.mirrorQueueDeleteOnExit {
		total++
	}
	if total == 0 {
		return summary, nil
	}
	step := 0

	if len(state.createdSubscriptionARNs) > 0 {
		step++
		ok := true
		if !autoApprove {
			var err error
			ok, err = prompt.confirmTeardown(step, total, fmt.Sprintf("unsubscribe %d session-created mirror subscription(s)", len(state.createdSubscriptionARNs)))
			if err != nil {
				return summary, err
			}
		}
		if !ok {
			summary.SkippedUnsubscribe = len(state.createdSubscriptionARNs)
			summary.Retained = append(summary.Retained, fmt.Sprintf("%d subscription(s)", len(state.createdSubscriptionARNs)))
			printUnsubscribeCleanup(execCtx, state.createdSubscriptionARNs, out)
		} else {
			for _, subARN := range state.createdSubscriptionARNs {
				subARN = strings.TrimSpace(subARN)
				if subARN == "" {
					continue
				}
				_, err := snsCli.Unsubscribe(ctx, &sdksns.UnsubscribeInput{SubscriptionArn: &subARN})
				if err != nil {
					return summary, fmt.Errorf("unsubscribe %s: %w", subARN, err)
				}
				summary.Unsubscribed++
			}
		}
	}

	if state.mirrorQueueDeleteOnExit {
		step++
		ok := true
		desc := fmt.Sprintf("delete mirror queue %s", state.mirrorQueueName)
		if state.mirrorQueueCreated {
			desc = fmt.Sprintf("delete session-created mirror queue %s", state.mirrorQueueName)
		} else {
			desc = fmt.Sprintf("delete awscope-managed mirror queue %s", state.mirrorQueueName)
		}
		if !autoApprove {
			var err error
			ok, err = prompt.confirmTeardown(step, total, desc)
			if err != nil {
				return summary, err
			}
		}
		if !ok {
			summary.QueueDeleteSkipped = true
			summary.Retained = append(summary.Retained, "mirror queue")
			printDeleteQueueCleanup(execCtx, state.mirrorQueueURL, out)
		} else {
			q := state.mirrorQueueURL
			_, err := sqsCli.DeleteQueue(ctx, &sdksqs.DeleteQueueInput{QueueUrl: &q})
			if err != nil {
				return summary, fmt.Errorf("delete mirror queue: %w", err)
			}
			summary.QueueDeleted = true
		}
	}

	if len(summary.Retained) > 0 {
		fmt.Fprintf(errOut, "teardown retained resources: %s\n", strings.Join(summary.Retained, ", "))
	}
	return summary, nil
}

func printUnsubscribeCleanup(execCtx actions.ExecContext, subARNs []string, out io.Writer) {
	for _, subARN := range subARNs {
		subARN = strings.TrimSpace(subARN)
		if subARN == "" {
			continue
		}
		fmt.Fprintf(out, "cleanup: aws sns unsubscribe --subscription-arn %s --region %s%s\n", subARN, execCtx.Region, profileArg(execCtx.Profile))
	}
}

func printDeleteQueueCleanup(execCtx actions.ExecContext, queueURL string, out io.Writer) {
	fmt.Fprintf(out, "cleanup: aws sqs delete-queue --queue-url %s --region %s%s\n", strings.TrimSpace(queueURL), execCtx.Region, profileArg(execCtx.Profile))
}

func profileArg(profile string) string {
	p := strings.TrimSpace(profile)
	if p == "" {
		return ""
	}
	return " --profile " + p
}

func nonNilReader(v io.Reader, fallback io.Reader) io.Reader {
	if v != nil {
		return v
	}
	return fallback
}

func nonNilWriter(v io.Writer, fallback io.Writer) io.Writer {
	if v != nil {
		return v
	}
	return fallback
}
