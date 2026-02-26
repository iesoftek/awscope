package sqs

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"awscope/internal/actions"
	"awscope/internal/graph"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdksns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	sdksqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
)

func TestMirrorStreamApplicable(t *testing.T) {
	a := MirrorStream{}
	if !a.Applicable(graph.ResourceNode{Service: "sqs", Type: "sqs:queue", Attributes: map[string]any{"url": "https://sqs.us-east-1.amazonaws.com/123/q"}}) {
		t.Fatalf("expected sqs queue with url to be applicable")
	}
	if a.Applicable(graph.ResourceNode{Service: "sqs", Type: "sqs:queue"}) {
		t.Fatalf("expected sqs queue without identity hints to be inapplicable")
	}
	if a.Applicable(graph.ResourceNode{Service: "sns", Type: "sns:topic", Arn: "arn:aws:sns:us-east-1:123:t"}) {
		t.Fatalf("expected non-sqs resource to be inapplicable")
	}
}

func TestMirrorQueueName(t *testing.T) {
	name := mirrorQueueName("orders", "arn:aws:sqs:us-east-1:123456789012:orders", false)
	if name != "orders-awscope-mirror" {
		t.Fatalf("unexpected name: %s", name)
	}
	fifo := mirrorQueueName("payments.fifo", "arn:aws:sqs:us-east-1:123456789012:payments.fifo", true)
	if !strings.HasSuffix(fifo, ".fifo") {
		t.Fatalf("fifo mirror must end with .fifo: %s", fifo)
	}
	long := strings.Repeat("x", 120)
	short := mirrorQueueName(long, "arn:aws:sqs:us-east-1:123456789012:"+long, false)
	if len(short) > 80 {
		t.Fatalf("mirror queue name must be <= 80 chars: len=%d", len(short))
	}
}

func TestMirrorStreamNoSNSPathRejected(t *testing.T) {
	oldSQS, oldSNS := newSQSClient, newSNSClient
	t.Cleanup(func() {
		newSQSClient = oldSQS
		newSNSClient = oldSNS
	})

	fSQS := newFakeSQS("us-east-1", "123456789012")
	sourceURL := "https://sqs.us-east-1.amazonaws.com/123456789012/orders"
	sourceARN := "arn:aws:sqs:us-east-1:123456789012:orders"
	fSQS.addQueue("orders", sourceURL, sourceARN, false)

	fSNS := &fakeSNS{}
	newSQSClient = func(cfg awsSDK.Config) sqsAPI { return fSQS }
	newSNSClient = func(cfg awsSDK.Config) snsAPI { return fSNS }

	a := MirrorStream{}
	_, err := a.ExecuteTerminal(context.Background(), actions.ExecContext{
		Region: "us-east-1",
		Stdin:  strings.NewReader("y\n"),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}, graph.ResourceNode{
		Service:     "sqs",
		Type:        "sqs:queue",
		DisplayName: "orders",
		Attributes:  map[string]any{"url": sourceURL},
	})
	if err == nil || !strings.Contains(err.Error(), "no SNS fanout") {
		t.Fatalf("expected no SNS fanout error, got %v", err)
	}
	if fSQS.receiveCalls != 0 {
		t.Fatalf("should not stream from queue when discovery fails")
	}
}

func TestMirrorStreamRejectsAtDiscoveryConfirmation(t *testing.T) {
	oldSQS, oldSNS := newSQSClient, newSNSClient
	t.Cleanup(func() {
		newSQSClient = oldSQS
		newSNSClient = oldSNS
	})

	fSQS := newFakeSQS("us-east-1", "123456789012")
	sourceURL := "https://sqs.us-east-1.amazonaws.com/123456789012/orders"
	sourceARN := "arn:aws:sqs:us-east-1:123456789012:orders"
	fSQS.addQueue("orders", sourceURL, sourceARN, false)

	fSNS := &fakeSNS{
		subscriptions: []snstypes.Subscription{{
			Protocol: awsSDK.String("sqs"),
			Endpoint: awsSDK.String(sourceARN),
			TopicArn: awsSDK.String("arn:aws:sns:us-east-1:123456789012:events"),
		}},
	}

	newSQSClient = func(cfg awsSDK.Config) sqsAPI { return fSQS }
	newSNSClient = func(cfg awsSDK.Config) snsAPI { return fSNS }

	a := MirrorStream{}
	_, err := a.ExecuteTerminal(context.Background(), actions.ExecContext{
		Region: "us-east-1",
		Stdin:  strings.NewReader("n\n"),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}, graph.ResourceNode{Service: "sqs", Type: "sqs:queue", Attributes: map[string]any{"url": sourceURL}})
	if err == nil || !strings.Contains(err.Error(), "aborted by user") {
		t.Fatalf("expected user-aborted error, got %v", err)
	}
	if len(fSQS.createdQueues) != 0 {
		t.Fatalf("queue creation should not happen when discovery confirm is rejected")
	}
}

func TestMirrorStreamCreatesAndTearsDownSessionResources(t *testing.T) {
	oldSQS, oldSNS := newSQSClient, newSNSClient
	t.Cleanup(func() {
		newSQSClient = oldSQS
		newSNSClient = oldSNS
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fSQS := newFakeSQS("us-east-1", "123456789012")
	sourceURL := "https://sqs.us-east-1.amazonaws.com/123456789012/orders"
	sourceARN := "arn:aws:sqs:us-east-1:123456789012:orders"
	fSQS.addQueue("orders", sourceURL, sourceARN, false)
	fSQS.receiveOutputs = []*sdksqs.ReceiveMessageOutput{{
		Messages: []sqstypes.Message{{
			MessageId:     awsSDK.String("m-1"),
			ReceiptHandle: awsSDK.String("r-1"),
			Body:          awsSDK.String(`{"TopicArn":"arn:aws:sns:us-east-1:123456789012:events","Message":"hello world"}`),
			Attributes: map[string]string{
				string(sqstypes.MessageSystemAttributeNameApproximateReceiveCount): "1",
				string(sqstypes.MessageSystemAttributeNameSentTimestamp):           "1730000000000",
			},
		}},
	}}
	fSQS.onReceive = func() { cancel() }

	topicARN := "arn:aws:sns:us-east-1:123456789012:events"
	fSNS := &fakeSNS{
		subscriptions: []snstypes.Subscription{{
			Protocol: awsSDK.String("sqs"),
			Endpoint: awsSDK.String(sourceARN),
			TopicArn: awsSDK.String(topicARN),
		}},
		subsByTopic: map[string][]snstypes.Subscription{topicARN: {}},
	}

	newSQSClient = func(cfg awsSDK.Config) sqsAPI { return fSQS }
	newSNSClient = func(cfg awsSDK.Config) snsAPI { return fSNS }

	in := strings.NewReader("y\ny\ny\ny\ny\ny\ny\n")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	a := MirrorStream{}
	res, err := a.ExecuteTerminal(ctx, actions.ExecContext{Region: "us-east-1", Profile: "default", Stdin: in, Stdout: stdout, Stderr: stderr}, graph.ResourceNode{Service: "sqs", Type: "sqs:queue", Attributes: map[string]any{"url": sourceURL}})
	if err != nil {
		t.Fatalf("ExecuteTerminal: %v", err)
	}
	if len(fSQS.createdQueues) != 1 {
		t.Fatalf("expected one mirror queue creation, got %d", len(fSQS.createdQueues))
	}
	if len(fSNS.createdSubs) != 1 {
		t.Fatalf("expected one mirror subscription, got %d", len(fSNS.createdSubs))
	}
	if len(fSNS.unsubscribed) != 1 {
		t.Fatalf("expected one unsubscribe in teardown, got %d", len(fSNS.unsubscribed))
	}
	if len(fSQS.deletedQueues) != 1 {
		t.Fatalf("expected mirror queue deletion in teardown, got %d", len(fSQS.deletedQueues))
	}
	if got := strings.TrimSpace(stdout.String()); !strings.Contains(got, "streaming from mirror queue") {
		t.Fatalf("expected stream output, got %q", got)
	}
	if res.Data == nil || res.Data["mirror_queue_created"] != true {
		t.Fatalf("expected mirror_queue_created=true, got %#v", res.Data)
	}
}

func TestMirrorStreamKeepsPreExistingMirrorQueue(t *testing.T) {
	oldSQS, oldSNS := newSQSClient, newSNSClient
	t.Cleanup(func() {
		newSQSClient = oldSQS
		newSNSClient = oldSNS
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fSQS := newFakeSQS("us-east-1", "123456789012")
	sourceURL := "https://sqs.us-east-1.amazonaws.com/123456789012/orders"
	sourceARN := "arn:aws:sqs:us-east-1:123456789012:orders"
	fSQS.addQueue("orders", sourceURL, sourceARN, false)
	mirrorName := mirrorQueueName("orders", sourceARN, false)
	mirrorURL := "https://sqs.us-east-1.amazonaws.com/123456789012/" + mirrorName
	mirrorARN := "arn:aws:sqs:us-east-1:123456789012:" + mirrorName
	fSQS.addQueue(mirrorName, mirrorURL, mirrorARN, false)
	fSQS.receiveOutputs = []*sdksqs.ReceiveMessageOutput{{}}
	fSQS.onReceive = func() { cancel() }

	topicARN := "arn:aws:sns:us-east-1:123456789012:events"
	fSNS := &fakeSNS{
		subscriptions: []snstypes.Subscription{{
			Protocol: awsSDK.String("sqs"),
			Endpoint: awsSDK.String(sourceARN),
			TopicArn: awsSDK.String(topicARN),
		}},
		subsByTopic: map[string][]snstypes.Subscription{topicARN: {}},
	}

	newSQSClient = func(cfg awsSDK.Config) sqsAPI { return fSQS }
	newSNSClient = func(cfg awsSDK.Config) snsAPI { return fSNS }

	in := strings.NewReader("y\ny\ny\ny\ny\n")
	a := MirrorStream{}
	_, err := a.ExecuteTerminal(ctx, actions.ExecContext{Region: "us-east-1", Profile: "default", Stdin: in, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, graph.ResourceNode{Service: "sqs", Type: "sqs:queue", Attributes: map[string]any{"url": sourceURL}})
	if err != nil {
		t.Fatalf("ExecuteTerminal: %v", err)
	}
	if len(fSQS.deletedQueues) != 0 {
		t.Fatalf("expected pre-existing mirror queue to be preserved")
	}
	if len(fSNS.unsubscribed) != 1 {
		t.Fatalf("expected session-created subscription unsubscribe, got %d", len(fSNS.unsubscribed))
	}
}

func TestMirrorStreamDeletesPreExistingAwscopeManagedQueueOnCancel(t *testing.T) {
	oldSQS, oldSNS := newSQSClient, newSNSClient
	t.Cleanup(func() {
		newSQSClient = oldSQS
		newSNSClient = oldSNS
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fSQS := newFakeSQS("us-east-1", "123456789012")
	sourceURL := "https://sqs.us-east-1.amazonaws.com/123456789012/orders"
	sourceARN := "arn:aws:sqs:us-east-1:123456789012:orders"
	fSQS.addQueue("orders", sourceURL, sourceARN, false)
	mirrorName := mirrorQueueName("orders", sourceARN, false)
	mirrorURL := "https://sqs.us-east-1.amazonaws.com/123456789012/" + mirrorName
	mirrorARN := "arn:aws:sqs:us-east-1:123456789012:" + mirrorName
	fSQS.addQueue(mirrorName, mirrorURL, mirrorARN, false)
	fSQS.queueTags[mirrorURL] = map[string]string{
		"awscope:managed_by": "awscope",
		"awscope:mirror_for": sourceARN,
	}
	fSQS.receiveOutputs = []*sdksqs.ReceiveMessageOutput{{}}
	fSQS.onReceive = func() { cancel() }

	topicARN := "arn:aws:sns:us-east-1:123456789012:events"
	fSNS := &fakeSNS{
		subscriptions: []snstypes.Subscription{{
			Protocol: awsSDK.String("sqs"),
			Endpoint: awsSDK.String(sourceARN),
			TopicArn: awsSDK.String(topicARN),
		}},
		subsByTopic: map[string][]snstypes.Subscription{
			topicARN: {{
				Protocol:        awsSDK.String("sqs"),
				Endpoint:        awsSDK.String(mirrorARN),
				SubscriptionArn: awsSDK.String("arn:aws:sns:us-east-1:123456789012:sub/existing"),
			}},
		},
	}

	newSQSClient = func(cfg awsSDK.Config) sqsAPI { return fSQS }
	newSNSClient = func(cfg awsSDK.Config) snsAPI { return fSNS }

	in := strings.NewReader("y\ny\ny\ny\ny\n")
	a := MirrorStream{}
	_, err := a.ExecuteTerminal(ctx, actions.ExecContext{
		Region:                      "us-east-1",
		Profile:                     "default",
		Stdin:                       in,
		Stdout:                      &bytes.Buffer{},
		Stderr:                      &bytes.Buffer{},
		AutoApproveTeardownOnCancel: true,
	}, graph.ResourceNode{Service: "sqs", Type: "sqs:queue", Attributes: map[string]any{"url": sourceURL}})
	if err != nil {
		t.Fatalf("ExecuteTerminal: %v", err)
	}
	if len(fSQS.deletedQueues) != 1 {
		t.Fatalf("expected managed mirror queue deleted on cancel, got %d", len(fSQS.deletedQueues))
	}
}

func TestTeardownSessionAutoApproveOnCancel(t *testing.T) {
	fSQS := newFakeSQS("us-east-1", "123456789012")
	fSNS := &fakeSNS{subsByTopic: map[string][]snstypes.Subscription{}}
	state := &sessionState{
		mirrorQueueCreated:      true,
		mirrorQueueDeleteOnExit: true,
		mirrorQueueName:         "orders-awscope-mirror",
		mirrorQueueURL:          "https://sqs.us-east-1.amazonaws.com/123456789012/orders-awscope-mirror",
		createdSubscriptionARNs: []string{"arn:aws:sns:us-east-1:123456789012:sub/1"},
	}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	prompt := newPrompter(nil, out)

	sum, err := teardownSession(context.Background(), prompt, fSQS, fSNS, actions.ExecContext{
		Region:  "us-east-1",
		Profile: "default",
	}, state, out, errOut, true)
	if err != nil {
		t.Fatalf("teardownSession: %v", err)
	}
	if sum.Unsubscribed != 1 {
		t.Fatalf("expected one unsubscribe, got %d", sum.Unsubscribed)
	}
	if !sum.QueueDeleted {
		t.Fatalf("expected mirror queue delete")
	}
	if len(fSNS.unsubscribed) != 1 {
		t.Fatalf("expected one unsubscribed arn, got %d", len(fSNS.unsubscribed))
	}
	if len(fSQS.deletedQueues) != 1 {
		t.Fatalf("expected one deleted queue, got %d", len(fSQS.deletedQueues))
	}
}

func TestTeardownSessionPromptRequiredByDefault(t *testing.T) {
	fSQS := newFakeSQS("us-east-1", "123456789012")
	fSNS := &fakeSNS{subsByTopic: map[string][]snstypes.Subscription{}}
	state := &sessionState{
		mirrorQueueCreated:      true,
		mirrorQueueDeleteOnExit: true,
		mirrorQueueName:         "orders-awscope-mirror",
		mirrorQueueURL:          "https://sqs.us-east-1.amazonaws.com/123456789012/orders-awscope-mirror",
		createdSubscriptionARNs: []string{"arn:aws:sns:us-east-1:123456789012:sub/1"},
	}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	prompt := newPrompter(nil, out)

	_, err := teardownSession(context.Background(), prompt, fSQS, fSNS, actions.ExecContext{
		Region:  "us-east-1",
		Profile: "default",
	}, state, out, errOut, false)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "interactive confirmation required") {
		t.Fatalf("expected interactive confirmation error, got %v", err)
	}
}

type fakeSQS struct {
	region string
	acct   string

	queueURLByName map[string]string
	queueAttrs     map[string]map[string]string
	queueTags      map[string]map[string]string

	createdQueues []string
	deletedQueues []string

	receiveOutputs []*sdksqs.ReceiveMessageOutput
	receiveCalls   int
	onReceive      func()
}

func newFakeSQS(region, acct string) *fakeSQS {
	return &fakeSQS{
		region:         region,
		acct:           acct,
		queueURLByName: map[string]string{},
		queueAttrs:     map[string]map[string]string{},
		queueTags:      map[string]map[string]string{},
	}
}

func (f *fakeSQS) addQueue(name, url, arn string, fifo bool) {
	f.queueURLByName[name] = url
	f.queueAttrs[url] = map[string]string{
		string(sqstypes.QueueAttributeNameQueueArn): arn,
		string(sqstypes.QueueAttributeNameFifoQueue): func() string {
			if fifo {
				return "true"
			}
			return "false"
		}(),
		string(sqstypes.QueueAttributeNameMessageRetentionPeriod): "345600",
	}
	f.queueTags[url] = map[string]string{}
}

func (f *fakeSQS) GetQueueAttributes(ctx context.Context, params *sdksqs.GetQueueAttributesInput, optFns ...func(*sdksqs.Options)) (*sdksqs.GetQueueAttributesOutput, error) {
	attrs, ok := f.queueAttrs[awsSDK.ToString(params.QueueUrl)]
	if !ok {
		return nil, fakeAPIError{code: "AWS.SimpleQueueService.NonExistentQueue", msg: "missing queue"}
	}
	out := map[string]string{}
	for k, v := range attrs {
		out[k] = v
	}
	return &sdksqs.GetQueueAttributesOutput{Attributes: out}, nil
}

func (f *fakeSQS) GetQueueUrl(ctx context.Context, params *sdksqs.GetQueueUrlInput, optFns ...func(*sdksqs.Options)) (*sdksqs.GetQueueUrlOutput, error) {
	name := awsSDK.ToString(params.QueueName)
	url, ok := f.queueURLByName[name]
	if !ok {
		return nil, fakeAPIError{code: "AWS.SimpleQueueService.NonExistentQueue", msg: "not found"}
	}
	return &sdksqs.GetQueueUrlOutput{QueueUrl: awsSDK.String(url)}, nil
}

func (f *fakeSQS) CreateQueue(ctx context.Context, params *sdksqs.CreateQueueInput, optFns ...func(*sdksqs.Options)) (*sdksqs.CreateQueueOutput, error) {
	name := awsSDK.ToString(params.QueueName)
	url := fmt.Sprintf("https://sqs.%s.amazonaws.com/%s/%s", f.region, f.acct, name)
	arn := fmt.Sprintf("arn:aws:sqs:%s:%s:%s", f.region, f.acct, name)
	f.queueURLByName[name] = url
	attrs := map[string]string{
		string(sqstypes.QueueAttributeNameQueueArn): arn,
		string(sqstypes.QueueAttributeNamePolicy):   "",
	}
	for k, v := range params.Attributes {
		attrs[k] = v
	}
	f.queueAttrs[url] = attrs
	tagCopy := map[string]string{}
	for k, v := range params.Tags {
		tagCopy[k] = v
	}
	f.queueTags[url] = tagCopy
	f.createdQueues = append(f.createdQueues, name)
	return &sdksqs.CreateQueueOutput{QueueUrl: awsSDK.String(url)}, nil
}

func (f *fakeSQS) ListQueueTags(ctx context.Context, params *sdksqs.ListQueueTagsInput, optFns ...func(*sdksqs.Options)) (*sdksqs.ListQueueTagsOutput, error) {
	url := awsSDK.ToString(params.QueueUrl)
	tags, ok := f.queueTags[url]
	if !ok {
		return nil, fakeAPIError{code: "AWS.SimpleQueueService.NonExistentQueue", msg: "missing queue"}
	}
	out := map[string]string{}
	for k, v := range tags {
		out[k] = v
	}
	return &sdksqs.ListQueueTagsOutput{Tags: out}, nil
}

func (f *fakeSQS) SetQueueAttributes(ctx context.Context, params *sdksqs.SetQueueAttributesInput, optFns ...func(*sdksqs.Options)) (*sdksqs.SetQueueAttributesOutput, error) {
	url := awsSDK.ToString(params.QueueUrl)
	attrs, ok := f.queueAttrs[url]
	if !ok {
		return nil, fakeAPIError{code: "AWS.SimpleQueueService.NonExistentQueue", msg: "missing queue"}
	}
	for k, v := range params.Attributes {
		attrs[k] = v
	}
	f.queueAttrs[url] = attrs
	return &sdksqs.SetQueueAttributesOutput{}, nil
}

func (f *fakeSQS) ReceiveMessage(ctx context.Context, params *sdksqs.ReceiveMessageInput, optFns ...func(*sdksqs.Options)) (*sdksqs.ReceiveMessageOutput, error) {
	f.receiveCalls++
	if f.onReceive != nil {
		f.onReceive()
		f.onReceive = nil
	}
	if len(f.receiveOutputs) == 0 {
		return &sdksqs.ReceiveMessageOutput{}, nil
	}
	out := f.receiveOutputs[0]
	f.receiveOutputs = f.receiveOutputs[1:]
	return out, nil
}

func (f *fakeSQS) DeleteMessageBatch(ctx context.Context, params *sdksqs.DeleteMessageBatchInput, optFns ...func(*sdksqs.Options)) (*sdksqs.DeleteMessageBatchOutput, error) {
	return &sdksqs.DeleteMessageBatchOutput{}, nil
}

func (f *fakeSQS) DeleteQueue(ctx context.Context, params *sdksqs.DeleteQueueInput, optFns ...func(*sdksqs.Options)) (*sdksqs.DeleteQueueOutput, error) {
	f.deletedQueues = append(f.deletedQueues, awsSDK.ToString(params.QueueUrl))
	return &sdksqs.DeleteQueueOutput{}, nil
}

type fakeSNS struct {
	subscriptions []snstypes.Subscription
	subsByTopic   map[string][]snstypes.Subscription
	createdSubs   []string
	unsubscribed  []string
}

func (f *fakeSNS) ListSubscriptions(ctx context.Context, params *sdksns.ListSubscriptionsInput, optFns ...func(*sdksns.Options)) (*sdksns.ListSubscriptionsOutput, error) {
	return &sdksns.ListSubscriptionsOutput{Subscriptions: append([]snstypes.Subscription(nil), f.subscriptions...)}, nil
}

func (f *fakeSNS) ListSubscriptionsByTopic(ctx context.Context, params *sdksns.ListSubscriptionsByTopicInput, optFns ...func(*sdksns.Options)) (*sdksns.ListSubscriptionsByTopicOutput, error) {
	topic := awsSDK.ToString(params.TopicArn)
	return &sdksns.ListSubscriptionsByTopicOutput{Subscriptions: append([]snstypes.Subscription(nil), f.subsByTopic[topic]...)}, nil
}

func (f *fakeSNS) Subscribe(ctx context.Context, params *sdksns.SubscribeInput, optFns ...func(*sdksns.Options)) (*sdksns.SubscribeOutput, error) {
	topic := strings.TrimSpace(awsSDK.ToString(params.TopicArn))
	endpoint := strings.TrimSpace(awsSDK.ToString(params.Endpoint))
	arn := fmt.Sprintf("arn:aws:sns:us-east-1:123456789012:sub/%d", len(f.createdSubs)+1)
	f.createdSubs = append(f.createdSubs, arn)
	sub := snstypes.Subscription{
		SubscriptionArn: awsSDK.String(arn),
		TopicArn:        awsSDK.String(topic),
		Endpoint:        awsSDK.String(endpoint),
		Protocol:        awsSDK.String("sqs"),
	}
	f.subsByTopic[topic] = append(f.subsByTopic[topic], sub)
	return &sdksns.SubscribeOutput{SubscriptionArn: awsSDK.String(arn)}, nil
}

func (f *fakeSNS) Unsubscribe(ctx context.Context, params *sdksns.UnsubscribeInput, optFns ...func(*sdksns.Options)) (*sdksns.UnsubscribeOutput, error) {
	f.unsubscribed = append(f.unsubscribed, awsSDK.ToString(params.SubscriptionArn))
	return &sdksns.UnsubscribeOutput{}, nil
}

type fakeAPIError struct {
	code string
	msg  string
}

func (e fakeAPIError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return e.code
}
func (e fakeAPIError) ErrorCode() string    { return e.code }
func (e fakeAPIError) ErrorMessage() string { return e.msg }
func (e fakeAPIError) ErrorFault() smithy.ErrorFault {
	return smithy.FaultClient
}
