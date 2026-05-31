package modelprovider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

type ErrorClass string

const (
	ErrorClassConfigError          ErrorClass = "config_error"
	ErrorClassAuthError            ErrorClass = "auth_error"
	ErrorClassBillingError         ErrorClass = "billing_error"
	ErrorClassRequestError         ErrorClass = "request_error"
	ErrorClassRateLimited          ErrorClass = "rate_limited"
	ErrorClassProviderUnavailable  ErrorClass = "provider_unavailable"
	ErrorClassNetworkError         ErrorClass = "network_error"
	ErrorClassToolSchemaError      ErrorClass = "tool_schema_error"
	ErrorClassUnknownProviderError ErrorClass = "provider_error"
)

type ProviderError struct {
	Class      ErrorClass
	StatusCode int
	Retryable  bool
	Message    string
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "model provider request failed"
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("model provider %s (status=%d): %s", e.Class, e.StatusCode, message)
	}
	return fmt.Sprintf("model provider %s: %s", e.Class, message)
}

func ProviderErrorForStatus(statusCode int, message string, redactor Redactor) *ProviderError {
	class := ClassifyHTTPStatus(statusCode)
	return &ProviderError{
		Class:      class,
		StatusCode: statusCode,
		Retryable:  IsRetryableErrorClass(class),
		Message:    redactor.RedactString(strings.TrimSpace(message)),
	}
}

func ClassifyHTTPStatus(statusCode int) ErrorClass {
	switch statusCode {
	case http.StatusUnauthorized:
		return ErrorClassAuthError
	case http.StatusPaymentRequired:
		return ErrorClassBillingError
	case http.StatusTooManyRequests:
		return ErrorClassRateLimited
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return ErrorClassProviderUnavailable
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return ErrorClassRequestError
	default:
		if statusCode >= 500 {
			return ErrorClassProviderUnavailable
		}
		if statusCode >= 400 {
			return ErrorClassRequestError
		}
		return ErrorClassUnknownProviderError
	}
}

func ClassifyProviderError(err error) *ProviderError {
	if err == nil {
		return nil
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ProviderError{Class: ErrorClassNetworkError, Retryable: true, Message: err.Error()}
	}
	if errors.Is(err, context.Canceled) {
		return &ProviderError{Class: ErrorClassNetworkError, Retryable: false, Message: err.Error()}
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return &ProviderError{Class: ErrorClassNetworkError, Retryable: true, Message: urlErr.Err.Error()}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return &ProviderError{Class: ErrorClassNetworkError, Retryable: netErr.Timeout(), Message: netErr.Error()}
	}
	return &ProviderError{Class: ErrorClassUnknownProviderError, Retryable: false, Message: err.Error()}
}

func IsRetryableErrorClass(class ErrorClass) bool {
	return class == ErrorClassRateLimited || class == ErrorClassProviderUnavailable || class == ErrorClassNetworkError
}
