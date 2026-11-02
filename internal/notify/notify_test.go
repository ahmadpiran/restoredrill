package notify

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostErrorOmitsURL(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + "/r/secret-token"
	srv.Close()

	err := post(url, "application/json", []byte("{}"))
	if err == nil {
		t.Fatal("expected an error posting to a closed server")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Errorf("error leaks the URL: %v", err)
	}
}

func TestPostHTTPErrorOmitsURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	err := post(srv.URL+"/r/secret-token", "application/json", []byte("{}"))
	if err == nil || err.Error() != "HTTP 502" {
		t.Errorf("got %v, want HTTP 502", err)
	}
}
