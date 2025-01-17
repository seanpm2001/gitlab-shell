package testserver

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitlab-shell/v14/internal/testhelper"
)

type TestRequestHandler struct {
	Path    string
	Handler func(w http.ResponseWriter, r *http.Request)
}

func StartSocketHttpServer(t *testing.T, handlers []TestRequestHandler) string {
	t.Helper()

	tempDir, _ := os.MkdirTemp("", "gitlab-shell-test-api")
	t.Cleanup(func() { os.RemoveAll(tempDir) })

	testSocket := path.Join(tempDir, "internal.sock")
	err := os.MkdirAll(filepath.Dir(testSocket), 0700)
	require.NoError(t, err)

	socketListener, err := net.Listen("unix", testSocket)
	require.NoError(t, err)

	server := http.Server{
		Handler: buildHandler(handlers),
		// We'll put this server through some nasty stuff we don't want
		// in our test output
		ErrorLog: log.New(io.Discard, "", 0),
	}
	go server.Serve(socketListener)

	url := "http+unix://" + testSocket

	return url
}

func StartHttpServer(t *testing.T, handlers []TestRequestHandler) string {
	t.Helper()

	server := httptest.NewServer(buildHandler(handlers))
	t.Cleanup(func() { server.Close() })

	return server.URL
}

func StartRetryHttpServer(t *testing.T, handlers []TestRequestHandler) string {
	attempts := map[string]int{}

	retryMiddileware := func(next func(w http.ResponseWriter, r *http.Request)) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts[r.URL.String()+r.Method]++
			if attempts[r.URL.String()+r.Method] == 1 {
				w.WriteHeader(500)
				return
			}

			http.HandlerFunc(next).ServeHTTP(w, r)
		})
	}
	t.Helper()

	h := http.NewServeMux()

	for _, handler := range handlers {
		h.Handle(handler.Path, retryMiddileware(handler.Handler))
	}

	server := httptest.NewServer(h)
	t.Cleanup(func() { server.Close() })

	return server.URL
}

func StartHttpsServer(t *testing.T, handlers []TestRequestHandler, clientCAPath string) string {
	t.Helper()

	crt := path.Join(testhelper.TestRoot, "certs/valid/server.crt")
	key := path.Join(testhelper.TestRoot, "certs/valid/server.key")

	server := httptest.NewUnstartedServer(buildHandler(handlers))
	cer, err := tls.LoadX509KeyPair(crt, key)
	require.NoError(t, err)

	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{cer},
		MinVersion:   tls.VersionTLS12,
	}

	if clientCAPath != "" {
		caCert, err := os.ReadFile(clientCAPath)
		require.NoError(t, err)

		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)

		server.TLS.ClientCAs = caCertPool
		server.TLS.ClientAuth = tls.RequireAndVerifyClientCert
	}

	server.StartTLS()

	t.Cleanup(func() { server.Close() })

	return server.URL
}

func buildHandler(handlers []TestRequestHandler) http.Handler {
	h := http.NewServeMux()

	for _, handler := range handlers {
		h.HandleFunc(handler.Path, handler.Handler)
	}

	return h
}
