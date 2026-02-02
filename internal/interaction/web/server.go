package web

import (
	"GMHA-MySQL/internal/service"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Server struct {
	svc service.ClusterService
}

func Run(svc service.ClusterService, port int) {
	s := &Server{svc: svc}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/clusters", s.handleClusters)
	mux.HandleFunc("/api/clusters/create", s.handleCreateCluster)

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Starting Web Server at %s...\n", addr)
	
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Printf("Web server error: %v\n", err)
	}
}

func (s *Server) handleClusters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clusters, err := s.svc.ListClusters(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clusters)
}

func (s *Server) handleCreateCluster(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input service.CreateClusterInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cluster, err := s.svc.CreateCluster(ctx, input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(cluster)
}
