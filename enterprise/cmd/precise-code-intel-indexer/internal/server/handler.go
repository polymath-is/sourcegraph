package server

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/inconshreveable/log15"
	indexmanager "github.com/sourcegraph/sourcegraph/enterprise/cmd/precise-code-intel-indexer/internal/index_manager"
)

func (s *Server) handler() http.Handler {
	mux := mux.NewRouter()
	mux.Path("/dequeue").Methods("POST").HandlerFunc(s.handleDequeue)
	mux.Path("/complete").Methods("POST").HandlerFunc(s.handleComplete)
	mux.Path("/heartbeat").Methods("POST").HandlerFunc(s.handleHeartbeat)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// POST /dequeue
func (s *Server) handleDequeue(w http.ResponseWriter, r *http.Request) {
	var payload indexmanager.DequeueRequest
	if !decodeBody(w, r, &payload) {
		return
	}

	index, dequeued, err := s.indexManager.Dequeue(r.Context(), payload)
	if err != nil {
		log15.Error("Failed to dequeue index", "err", err)
		http.Error(w, fmt.Sprintf("failed to dequeue index: %s", err.Error()), http.StatusInternalServerError)
		return
	}
	if !dequeued {
		fmt.Printf("WTH\n")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	fmt.Printf("Index: %v\n", index)
	writeJSON(w, index)
}

// POST /complete
func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	var payload indexmanager.CompleteRequest
	if !decodeBody(w, r, &payload) {
		return
	}

	found, err := s.indexManager.Complete(r.Context(), payload)
	if err != nil {
		log15.Error("Failed to complete index job", "err", err)
		http.Error(w, fmt.Sprintf("failed to complete index job: %s", err.Error()), http.StatusInternalServerError)
		return
	}
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// POST /heartbeat
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var payload indexmanager.HeartbeatRequest
	if !decodeBody(w, r, &payload) {
		return
	}

	s.indexManager.Heartbeat(payload)
	w.WriteHeader(http.StatusNoContent)
}
