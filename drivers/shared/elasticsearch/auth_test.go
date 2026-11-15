package elasticsearch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAPIKeyAuthenticatesEveryOperation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("Authorization"); got != "ApiKey encoded-test-key" {
			t.Errorf("unexpected authorization: %q", got)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/_search"):
			fmt.Fprint(w, `{"hits":{"total":{"value":1}}}`)
		case r.URL.Path == "/_bulk":
			fmt.Fprint(w, `{"items":[{"index":{}}]}`)
		default:
			fmt.Fprint(w, `{}`)
		}
	}))
	defer server.Close()
	// An API key takes precedence over URL basic-auth credentials.
	address := strings.Replace(server.URL, "https://", "https://user:password@", 1)
	backend, err := New(address, DriverConfig{APIKey: "encoded-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	d := backend.(*Driver)
	d.client.Transport = server.Client().Transport
	ctx := context.Background()
	if err := d.Exec(ctx, `{"index":"docs","mappings":{}}`); err != nil {
		t.Fatal(err)
	}
	if err := d.Exec(ctx, `[{"index":"docs","endpoint":"_refresh","timeout":"5s"}]`); err != nil {
		t.Fatal(err)
	}
	if n, err := d.Query(ctx, `{"query":{"match_all":{}}}`, "docs"); err != nil || n != 1 {
		t.Fatalf("query: hits=%d err=%v", n, err)
	}
	if n, err := d.Insert(ctx, "docs", []string{"id"}, [][]any{{1}}); err != nil || n != 1 {
		t.Fatalf("insert: count=%d err=%v", n, err)
	}
	if n, err := d.Update(ctx, "docs", []string{"id"}, []string{"id"}, [][]any{{1}}); err != nil || n != 1 {
		t.Fatalf("update: count=%d err=%v", n, err)
	}
	d.CaptureConfig(ctx, "elasticsearch-auth-test")
	if got := requests.Load(); got != 7 {
		t.Fatalf("received %d requests, want 7", got)
	}
}

func TestAPIKeyDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Store(true)
	}))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	backend, err := New(server.URL, DriverConfig{APIKey: "encoded-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	d := backend.(*Driver)
	d.client.Transport = server.Client().Transport
	if err := d.Exec(context.Background(), `[{"index":"docs","endpoint":"_refresh"}]`); err == nil {
		t.Fatal("expected redirect to fail")
	}
	if redirected.Load() {
		t.Fatal("followed an authenticated redirect")
	}
}
