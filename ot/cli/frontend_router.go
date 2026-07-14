package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

type frontendRouter struct {
	marketing *url.URL
	auth      *url.URL
	app       *url.URL
	docs      *url.URL
}

func newFrontendRouter() *frontendRouter {
	return &frontendRouter{
		marketing: mustURL("http://localhost:" + getenvDefault("MARKETING_PORT", "3005")),
		auth:      mustURL("http://localhost:" + getenvDefault("AUTH_WEB_PORT", "3006")),
		app:       mustURL("http://localhost:" + getenvDefault("APP_WEB_PORT", "3007")),
		docs:      mustURL("http://localhost:" + getenvDefault("DOCS_PORT", "3008")),
	}
}

func mustURL(raw string) *url.URL {
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return parsed
}

func (r *frontendRouter) resolveTarget(path string) *url.URL {
	if path == "" {
		return r.marketing
	}
	if path == "/auth" || strings.HasPrefix(path, "/auth/") {
		return r.auth
	}
	if path == "/app" || strings.HasPrefix(path, "/app/") {
		return r.app
	}
	if path == "/docs" || strings.HasPrefix(path, "/docs/") {
		return r.docs
	}
	return r.marketing
}

func (r *frontendRouter) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		target := r.resolveTarget(req.URL.Path)
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(rw http.ResponseWriter, r *http.Request, err error) {
			if rw.Header().Get("Content-Type") == "" {
				rw.Header().Set("Content-Type", "text/plain")
			}
			rw.WriteHeader(http.StatusBadGateway)
			_, _ = fmt.Fprintf(rw, "proxy error: %v", err)
		}
		proxy.ServeHTTP(w, req)
	})
}

func runFrontendRouter(ctx context.Context) error {
	port := os.Getenv("LOCAL_ROUTER_PORT")
	if port == "" {
		port = "3000"
	}
	addr := ":" + port

	router := newFrontendRouter()
	// No Read/WriteTimeout: the write deadline survives ReverseProxy's
	// connection hijack, so it would kill proxied WebSocket upgrades (Next.js
	// HMR on this router port) ~15s after connect. IdleTimeout is safe.
	server := &http.Server{
		Addr:        addr,
		Handler:     router.handler(),
		IdleTimeout: 30 * time.Second,
	}

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	fmt.Printf("Frontend router listening on http://localhost:%s (/, /auth, /app, /docs)\n", port)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
