package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"
)

// Server exposes a minimal HTTP API for managing operator resources.
// It implements manager.Runnable so controller-runtime starts it alongside controllers.
// TLS is terminated at the ingress/NLB layer; the server listens on plain HTTP.
type Server struct {
	handler http.Handler
	addr    string
}

func NewServer(addr, user, pass string, h *Handlers) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /redirects", h.list)
	mux.HandleFunc("POST /redirects", h.create)
	mux.HandleFunc("DELETE /redirects/{domain}", h.delete)
	return &Server{
		addr:    addr,
		handler: basicAuth(user, pass, mux),
	}
}

func (s *Server) Start(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background()) //nolint:contextcheck
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) NeedLeaderElection() bool { return false }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func basicAuth(user, pass string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="operator-api"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
