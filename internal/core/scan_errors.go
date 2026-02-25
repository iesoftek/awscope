package core

import (
	"errors"
	"net"
	"strings"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func isSkippableStepError(err error) bool {
	return isAccessDenied(err) || isEndpointUnavailable(err) || isRegionUnsupported(err)
}

func isAccessDenied(err error) bool {
	if err == nil {
		return false
	}
	// Some operations return 403 with inconsistent modeled error codes/messages across services.
	// Treat HTTP 401/403 as access denied for best-effort scans.
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.HTTPStatusCode() {
		case 401, 403:
			return true
		}
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "AccessDeniedException", "AccessDenied", "UnauthorizedOperation", "UnrecognizedClientException":
			return true
		}
	}
	// Fallback: some services wrap errors with these strings.
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "accessdenied") || strings.Contains(msg, "access denied") || strings.Contains(msg, "unauthorized") {
		return true
	}
	return false
}

func isEndpointUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var notFound *awsSDK.EndpointNotFoundError
	if errors.As(err, &notFound) {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "no such host"):
		return true
	case strings.Contains(msg, "endpoint not found"):
		return true
	case strings.Contains(msg, "unknown endpoint"):
		return true
	case strings.Contains(msg, "could not resolve endpoint"):
		return true
	default:
		return false
	}
}

func isRegionUnsupported(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := strings.TrimSpace(strings.ToLower(apiErr.ErrorCode()))
		switch code {
		case "unknownoperationexception", "unknownoperation", "unsupportedoperationexception", "unsupportedoperation":
			return true
		}
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "not supported in the called region"):
		return true
	case strings.Contains(msg, "is not supported in this region"):
		return true
	case strings.Contains(msg, "requested operation is not supported in the called region"):
		return true
	default:
		return false
	}
}
