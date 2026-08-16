package twitch

import (
	"fmt"
	"net/http"
)

// APIError represents an error from the Twitch API with structured information
type APIError struct {
	StatusCode int    // HTTP status code
	Message    string // Error message from the API
	Body       []byte // Raw response body (may be empty)
}

// Error implements the error interface
func (e *APIError) Error() string {
	if len(e.Body) > 0 && len(e.Body) < 500 {
		return fmt.Sprintf("API error: %d - %s (body: %s)", e.StatusCode, e.Message, string(e.Body))
	}
	return fmt.Sprintf("API error: %d - %s", e.StatusCode, e.Message)
}

// NewAPIError creates a new APIError
func NewAPIError(statusCode int, message string, body []byte) *APIError {
	return &APIError{
		StatusCode: statusCode,
		Message:    message,
		Body:       body,
	}
}

// NewAPIErrorFromResponse creates an APIError from an HTTP status code and response body
func NewAPIErrorFromResponse(statusCode int, body []byte) *APIError {
	return &APIError{
		StatusCode: statusCode,
		Message:    http.StatusText(statusCode),
		Body:       body,
	}
}

// IsAuthError returns true if this is an authentication error (401 Unauthorized)
func (e *APIError) IsAuthError() bool {
	return e.StatusCode == http.StatusUnauthorized
}

// IsRateLimited returns true if this is a rate limit error (429 Too Many Requests)
func (e *APIError) IsRateLimited() bool {
	return e.StatusCode == http.StatusTooManyRequests
}

// IsForbidden returns true if this is a forbidden error (403 Forbidden)
func (e *APIError) IsForbidden() bool {
	return e.StatusCode == http.StatusForbidden
}

// IsServerError returns true if this is a server error (5xx)
func (e *APIError) IsServerError() bool {
	return e.StatusCode >= 500 && e.StatusCode < 600
}

// IsRetryable returns true if this error might succeed on retry
// Rate limits and server errors are typically retryable
func (e *APIError) IsRetryable() bool {
	return e.IsRateLimited() || e.IsServerError()
}

// GetBody returns the response body as a string
func (e *APIError) GetBody() string {
	return string(e.Body)
}
