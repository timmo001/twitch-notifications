package auth

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestValidateRecordsExpiryAndRejectsBadTokens(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantErr   bool
		wantAuth  bool
		wantScope bool
	}{
		{"valid", 200, `{"client_id":"client","scopes":["user:read:follows"],"expires_in":3600}`, false, false, false},
		{"rejected", 401, `{"status":401,"message":"invalid access token"}`, true, true, false},
		{"other client", 200, `{"client_id":"other","scopes":["user:read:follows"],"expires_in":3600}`, true, true, false},
		{"missing scope", 200, `{"client_id":"client","scopes":[],"expires_in":3600}`, true, false, true},
		{"server failure", 503, `{}`, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "OAuth access" {
					t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			t.Cleanup(server.Close)
			tm := NewTokenManager("client", "secret", "access")
			tm.validateURL = server.URL
			tm.httpClient = server.Client()

			err := tm.Validate(t.Context())
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() = %v", err)
			}
			var oauthErr *OAuthError
			if isAuth := errors.As(err, &oauthErr) && oauthErr.IsAuthError(); isAuth != tc.wantAuth {
				t.Fatalf("auth error = %t, error = %v", isAuth, err)
			}
			if errors.Is(err, ErrMissingScope) != tc.wantScope {
				t.Fatalf("missing scope = %t, error = %v", !tc.wantScope, err)
			}
			if tc.wantErr != tm.ExpiresAt.IsZero() {
				t.Fatalf("ExpiresAt = %v", tm.ExpiresAt)
			}
		})
	}
}

func TestInvalidateForcesOneRefresh(t *testing.T) {
	tm := testTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)
	})
	tm.AccessToken = "rejected"

	// A token already replaced by another caller must not be invalidated.
	tm.Invalidate("older")
	if token, err := tm.GetAccessToken(t.Context()); err != nil || token != "rejected" {
		t.Fatalf("GetAccessToken() = %q, %v", token, err)
	}

	tm.Invalidate("rejected")
	if token, err := tm.GetAccessToken(t.Context()); err != nil || token != "new-access" {
		t.Fatalf("GetAccessToken() = %q, %v", token, err)
	}
	if !tm.ExpiresAt.After(time.Now()) {
		t.Fatalf("ExpiresAt = %v", tm.ExpiresAt)
	}
}
