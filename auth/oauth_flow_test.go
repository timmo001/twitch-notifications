package auth

import (
	"net"
	"testing"
)

func TestCallbackListensOnLoopbackOnly(t *testing.T) {
	listeners, err := listenLoopback("0")
	if err != nil {
		t.Fatal(err)
	}
	for _, listener := range listeners {
		defer listener.Close()
		host, _, err := net.SplitHostPort(listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			t.Fatalf("callback listens on %s", listener.Addr())
		}
	}
}
