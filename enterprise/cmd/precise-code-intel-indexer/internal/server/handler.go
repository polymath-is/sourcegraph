package server

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/inconshreveable/log15"
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
	indexerName := getQuery(r, "indexerName")

	index, dequeued, err := s.transactionManager.Dequeue(r.Context(), indexerName)
	if err != nil {
		log15.Error("Failed to dequeue index", "err", err)
		http.Error(w, fmt.Sprintf("failed to dequeue index: %s", err.Error()), http.StatusInternalServerError)
		return
	}
	if !dequeued {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	writeJSON(w, index)
}

// POST /complete
func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	// TODO - read from body instead
	indexerName := getQuery(r, "indexerName")
	indexID := getQueryInt(r, "indexId")
	indexErr := getQueryAsErr(r, "errorMessage")

	found, err := s.transactionManager.Complete(indexerName, indexID, indexErr)
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
	indexerName := getQuery(r, "indexerName")
	s.transactionManager.Heartbeat(indexerName)
	w.WriteHeader(http.StatusNoContent)
}
