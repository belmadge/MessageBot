package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/portfolio/whatsapp-support/internal/service"
)

type application interface {
	SimulateInbound(context.Context, string, string, string) (service.SimulationResult, error)
	ListConversations(context.Context, service.Status) ([]service.Conversation, error)
	GetConversation(context.Context, int64) (service.ConversationDetails, error)
	TakeoverConversation(context.Context, int64) (service.Conversation, error)
	SendHumanMessage(context.Context, int64, string) (service.Message, error)
	ResolveConversation(context.Context, int64) (service.Conversation, error)
}

type Handler struct {
	app application
}

func New(app application) http.Handler {
	h := &Handler{app: app}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("POST /api/simulated/whatsapp/messages", h.simulate)
	mux.HandleFunc("GET /api/conversations", h.list)
	mux.HandleFunc("GET /api/conversations/{id}", h.get)
	mux.HandleFunc("POST /api/conversations/{id}/takeover", h.takeover)
	mux.HandleFunc("POST /api/conversations/{id}/messages", h.sendHuman)
	mux.HandleFunc("POST /api/conversations/{id}/resolve", h.resolve)
	return mux
}

type inboundRequest struct {
	Phone      string `json:"phone"`
	Content    string `json:"content"`
	ExternalID string `json:"external_id"`
}

type messageRequest struct {
	Content string `json:"content"`
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) simulate(w http.ResponseWriter, r *http.Request) {
	var input inboundRequest
	if !decode(w, r, &input) {
		return
	}
	result, err := h.app.SimulateInbound(r.Context(), input.Phone, input.Content, input.ExternalID)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if result.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.app.ListConversations(r.Context(), service.Status(r.URL.Query().Get("status")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	item, err := h.app.GetConversation(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *Handler) takeover(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	conversation, err := h.app.TakeoverConversation(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conversation)
}

func (h *Handler) sendHuman(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var input messageRequest
	if !decode(w, r, &input) {
		return
	}
	item, err := h.app.SendHumanMessage(r.Context(), id, input.Content)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *Handler) resolve(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	item, err := h.app.ResolveConversation(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return false
	}
	return true
}

func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid conversation id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func writeError(w http.ResponseWriter, err error) {
	var providerFailure *service.AIProviderFailure
	if errors.As(err, &providerFailure) {
		log.Printf("AI provider failure: %v", providerFailure)
		if providerFailure.Kind == service.AIProviderFailureRateLimited {
			http.Error(w, "AI service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "AI provider request failed", http.StatusBadGateway)
		return
	}
	switch {
	case errors.Is(err, service.ErrInboundProcessing):
		http.Error(w, "message is still being processed; retry later", http.StatusServiceUnavailable)
	case errors.Is(err, service.ErrInvalidInput):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, service.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, service.ErrConversationResolved):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
