package utils

import "time"

// HTTPClientTimeout is the default timeout for HTTP client requests.
// This is used consistently across the application for API calls.
const HTTPClientTimeout = 10 * time.Second
