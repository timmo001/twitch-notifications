package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"twitch-notifications/utils"
)

const (
	// DefaultRedirectURI is the localhost callback URL for OAuth
	DefaultRedirectURI = "http://localhost:8080/oauth/callback"
	// DefaultCallbackPort is the port for the OAuth callback server
	DefaultCallbackPort = "8080"
	// RequiredScope is the OAuth scope needed for the application
	RequiredScope = "user:read:follows"

	// OAuth flow timing
	oauthServerShutdownTimeout = 5 * time.Second
	oauthFlowTimeout           = 5 * time.Minute
)

// OAuthFlow handles the complete OAuth authorization flow
type OAuthFlow struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Port         string
}

// NewOAuthFlow creates a new OAuth flow handler
func NewOAuthFlow(clientID, clientSecret string) *OAuthFlow {
	return &OAuthFlow{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  DefaultRedirectURI,
		Port:         DefaultCallbackPort,
	}
}

// Run executes the OAuth flow and returns the token manager
// isAutomatic indicates if this is running automatically due to invalid token
func (of *OAuthFlow) Run(ctx context.Context, isAutomatic bool) (*TokenManager, error) {
	// Generate state for CSRF protection
	state, err := generateState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}

	// Create channel to receive authorization code
	codeChan := make(chan string, 1)
	errorChan := make(chan error, 1)

	// Start local HTTP server for callback
	mux := http.NewServeMux()
	server := &http.Server{
		Addr:    ":" + of.Port,
		Handler: mux,
	}

	// Handle OAuth callback
	mux.HandleFunc("/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		// Verify state
		returnedState := r.URL.Query().Get("state")
		if returnedState != state {
			http.Error(w, "Invalid state parameter", http.StatusBadRequest)
			errorChan <- fmt.Errorf("invalid state parameter")
			return
		}

		// Check for error
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			errorDesc := r.URL.Query().Get("error_description")
			http.Error(w, fmt.Sprintf("OAuth error: %s - %s", errMsg, errorDesc), http.StatusBadRequest)
			errorChan <- fmt.Errorf("OAuth error: %s - %s", errMsg, errorDesc)
			return
		}

		// Get authorization code
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "No authorization code received", http.StatusBadRequest)
			errorChan <- fmt.Errorf("no authorization code received")
			return
		}

		// Send success response
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `
			<html>
			<head>
				<title>Authorization Successful</title>
				<script>window.onload = function() { window.close(); };</script>
			</head>
			<body>
				<h1>Authorization Successful!</h1>
				<p>You can close this window.</p>
			</body>
			</html>
		`)

		codeChan <- code
	})

	// Start server in goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errorChan <- fmt.Errorf("failed to start callback server: %w", err)
		}
	}()

	// Ensure server is closed when done
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), oauthServerShutdownTimeout)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	// Generate authorization URL
	authURL := GetAuthorizationURL(of.ClientID, of.RedirectURI, []string{RequiredScope})
	authURL += "&state=" + state

	// Open browser
	if isAutomatic {
		fmt.Println("🔐 Invalid or expired Twitch token detected.")
		fmt.Println("Opening browser for automatic re-authorization...")
		fmt.Println("This is required to continue receiving Twitch notifications.")
		fmt.Println()
	} else {
		fmt.Println("Opening browser for Twitch authorization...")
	}

	opener := utils.NewOpener()
	if err := opener.OpenAppURL(authURL); err != nil {
		fmt.Printf("Warning: Failed to open browser automatically: %v\n", err)
		fmt.Printf("Please open this URL in your browser:\n%s\n", authURL)
	}

	if isAutomatic {
		fmt.Println("Waiting for automatic re-authorization...")
		fmt.Println("(If the browser doesn't open, copy the URL above and open it manually)")
		fmt.Println("The application will continue automatically once authorization is complete.")
	} else {
		fmt.Println("Waiting for authorization...")
		fmt.Println("(If the browser doesn't open, copy the URL above and open it manually)")
	}

	// Wait for callback or timeout
	select {
	case code := <-codeChan:
		// Exchange code for token
		tm, err := ExchangeCodeForToken(ctx, of.ClientID, of.ClientSecret, code, of.RedirectURI)
		if err != nil {
			return nil, fmt.Errorf("failed to exchange code for token: %w", err)
		}
		return tm, nil

	case err := <-errorChan:
		return nil, err

	case <-ctx.Done():
		return nil, ctx.Err()

	case <-time.After(oauthFlowTimeout):
		return nil, fmt.Errorf("authorization timeout - please try again")
	}
}

// generateState generates a random state string for CSRF protection
func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
