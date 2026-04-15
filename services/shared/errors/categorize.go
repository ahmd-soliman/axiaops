package errors

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
)

// ErrorCategory represents different types of errors that can occur during scans
type ErrorCategory string

const (
	CategoryCredentials   ErrorCategory = "credentials"
	CategoryPermissions   ErrorCategory = "permissions"
	CategoryThrottling    ErrorCategory = "throttling"
	CategoryNetwork       ErrorCategory = "network"
	CategoryDataUnavail   ErrorCategory = "data_unavailable"
	CategoryInternal      ErrorCategory = "internal"
	CategoryUnknown       ErrorCategory = "unknown"
)

// CategorizedError wraps an error with category information
type CategorizedError struct {
	Category ErrorCategory
	Err      error
	Context  string
}

func (e *CategorizedError) Error() string {
	if e.Context != "" {
		return e.Context + ": " + e.Err.Error()
	}
	return e.Err.Error()
}

func (e *CategorizedError) Unwrap() error {
	return e.Err
}

// Categorize determines the category of an error for better handling
func Categorize(err error, context string) *CategorizedError {
	if err == nil {
		return nil
	}

	category := categorizeError(err)
	return &CategorizedError{
		Category: category,
		Err:      err,
		Context:  context,
	}
}

func categorizeError(err error) ErrorCategory {
	errStr := err.Error()

	// AWS-specific error patterns
	var dataUnavail *types.DataUnavailableException
	if errors.As(err, &dataUnavail) {
		return CategoryDataUnavail
	}

	// Credential errors
	credentialPatterns := []string{
		"InvalidUserID.NotFound",
		"AuthFailure",
		"SignatureDoesNotMatch",
		"TokenRefreshRequired",
		"ExpiredToken",
		"InvalidAccessKeyId",
		"InvalidSecretAccessKey",
	}
	for _, pattern := range credentialPatterns {
		if strings.Contains(errStr, pattern) {
			return CategoryCredentials
		}
	}

	// Permission errors
	permissionPatterns := []string{
		"AccessDenied",
		"UnauthorizedOperation",
		"Forbidden",
		"InsufficientPrivileges",
	}
	for _, pattern := range permissionPatterns {
		if strings.Contains(errStr, pattern) {
			return CategoryPermissions
		}
	}

	// Throttling errors
	throttlingPatterns := []string{
		"RequestLimitExceeded",
		"Throttling",
		"TooManyRequests",
		"LimitExceeded",
	}
	for _, pattern := range throttlingPatterns {
		if strings.Contains(errStr, pattern) {
			return CategoryThrottling
		}
	}

	// Network errors
	networkPatterns := []string{
		"connection reset",
		"timeout",
		"network is unreachable",
		"no such host",
		"connection refused",
	}
	for _, pattern := range networkPatterns {
		if strings.Contains(errStr, pattern) {
			return CategoryNetwork
		}
	}

	// Internal AWS errors
	internalPatterns := []string{
		"InternalError",
		"ServiceUnavailable",
		"InternalFailure",
	}
	for _, pattern := range internalPatterns {
		if strings.Contains(errStr, pattern) {
			return CategoryInternal
		}
	}

	return CategoryUnknown
}

// IsRetryable returns true if the error category suggests retrying might succeed
func (c ErrorCategory) IsRetryable() bool {
	switch c {
	case CategoryThrottling, CategoryNetwork, CategoryInternal:
		return true
	case CategoryCredentials, CategoryPermissions, CategoryDataUnavail:
		return false
	default:
		return false
	}
}

// ShouldFailScan returns true if this error should fail the entire scan
func (c ErrorCategory) ShouldFailScan() bool {
	switch c {
	case CategoryCredentials, CategoryPermissions:
		return true
	default:
		return false
	}
}