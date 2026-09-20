package twitch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type localHelixTransport struct {
	url       *url.URL
	transport http.RoundTripper
}

func TestHelixUserRequestCanBeCancelled(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := NewHelixClient("client", "token")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient.Transport = localHelixTransport{url: endpoint, transport: server.Client().Transport}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.GetUserID(ctx)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("GetUserID() = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request ignored cancellation")
	}
}

func (lt localHelixTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = lt.url.Scheme
	req.URL.Host = lt.url.Host
	return lt.transport.RoundTrip(req)
}

func TestHelixTokenRefreshDuringRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer token-") {
			t.Errorf("request has invalid authorisation header")
		}
		if r.URL.Path == "/helix/users" {
			fmt.Fprint(w, `{"data":[{"id":"1","login":"channel"}]}`)
		} else {
			fmt.Fprint(w, `{"data":[]}`)
		}
	}))
	defer server.Close()
	client, err := NewHelixClient("client", "token-initial")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient.Transport = localHelixTransport{url: endpoint, transport: server.Client().Transport}
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			client.UpdateAccessToken("token-refreshed")
		}
	})
	for range 10 {
		wg.Go(func() {
			if _, err := client.GetLiveStreams(t.Context(), []string{"1"}); err != nil {
				t.Error(err)
			}
			if _, err := client.GetUserID(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
