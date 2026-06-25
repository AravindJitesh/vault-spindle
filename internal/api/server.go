package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/aravind/vault-spindle/internal/catalog"
	"github.com/aravind/vault-spindle/internal/models"
	"github.com/aravind/vault-spindle/internal/store"
)

const maxBodyBytes = 1 << 20 // 1 MiB

type Server struct {
	store  *store.Store
	logger *slog.Logger
	mux    *http.ServeMux
}

func NewServer(st *store.Store, logger *slog.Logger) *Server {
	s := &Server{
		store:  st,
		logger: logger,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /v1/wallets/{playerId}", s.handleGetWallet)
	s.mux.HandleFunc("POST /v1/wallets/{playerId}/credit", s.handleCredit)
	s.mux.HandleFunc("POST /v1/wallets/{playerId}/purchase", s.handlePurchase)
	s.mux.HandleFunc("POST /v1/rewards/{rewardId}/claim", s.handleClaim)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, models.ErrorResponse{
			Error:   "unhealthy",
			Message: "database unreachable",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGetWallet(w http.ResponseWriter, r *http.Request) {
	playerID := r.PathValue("playerId")
	if err := models.ValidatePlayerID(playerID); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}

	balance, inventory, claimed, err := s.store.GetWallet(r.Context(), playerID)
	if err != nil {
		s.logger.Error("get wallet failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "internal_error", Message: "failed to read wallet"})
		return
	}

	writeJSON(w, http.StatusOK, models.WalletView{
		Balance:        balance,
		Inventory:      inventory,
		ClaimedRewards: claimed,
	})
}

func (s *Server) handleCredit(w http.ResponseWriter, r *http.Request) {
	playerID := r.PathValue("playerId")
	if err := models.ValidatePlayerID(playerID); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}

	idempotencyKey := r.Header.Get(models.IdempotencyKeyHeader)
	if err := models.ValidateIdempotencyKey(idempotencyKey); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}

	var req models.CreditRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}
	if err := models.ValidateAmount(req.Amount, "amount"); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}
	if err := models.ValidateNonEmptyString(req.Reason, "reason"); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}

	result, hit, err := s.store.Credit(r.Context(), playerID, idempotencyKey, req.Reason, req.Amount)
	if err != nil {
		s.logger.Error("credit failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "internal_error", Message: "credit failed"})
		return
	}
	if hit != nil {
		writeRawJSON(w, hit.HTTPStatus, hit.ResponseBody)
		return
	}

	writeJSON(w, http.StatusOK, models.CreditResponse{Balance: result.Balance, Reason: result.Reason})
}

func (s *Server) handlePurchase(w http.ResponseWriter, r *http.Request) {
	playerID := r.PathValue("playerId")
	if err := models.ValidatePlayerID(playerID); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}

	idempotencyKey := r.Header.Get(models.IdempotencyKeyHeader)
	if err := models.ValidateIdempotencyKey(idempotencyKey); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}

	var req models.PurchaseRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}
	if err := models.ValidateNonEmptyString(req.ItemID, "itemId"); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}
	if err := models.ValidateAmount(req.Price, "price"); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}

	price, err := catalog.AuthoritativePrice(req.ItemID, req.Price)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}

	result, hit, err := s.store.Purchase(r.Context(), playerID, idempotencyKey, req.ItemID, price)
	if err != nil {
		s.logger.Error("purchase failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "internal_error", Message: "purchase failed"})
		return
	}
	if hit != nil {
		writeRawJSON(w, hit.HTTPStatus, hit.ResponseBody)
		return
	}

	writeJSON(w, http.StatusOK, models.PurchaseResponse{
		Balance:   result.Balance,
		ItemID:    result.ItemID,
		Inventory: result.Inventory,
	})
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	rewardID := r.PathValue("rewardId")
	if err := models.ValidateNonEmptyString(rewardID, "rewardId"); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get(models.IdempotencyKeyHeader))

	var req models.ClaimRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}
	if err := models.ValidatePlayerID(req.PlayerID); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
		return
	}

	// Claim deduplication is enforced by UNIQUE(reward_id, player_id).
	// Idempotency-Key header is optional; when present, cached responses replay for retries.
	if idempotencyKey != "" {
		if err := models.ValidateIdempotencyKey(idempotencyKey); err != nil {
			writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid_request", Message: err.Error()})
			return
		}
	}

	result, hit, err := s.store.ClaimReward(r.Context(), rewardID, req.PlayerID, idempotencyKey)
	if err != nil {
		s.logger.Error("claim failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "internal_error", Message: "claim failed"})
		return
	}
	if hit != nil {
		writeRawJSON(w, hit.HTTPStatus, hit.ResponseBody)
		return
	}

	writeJSON(w, http.StatusOK, models.ClaimResponse{
		RewardID:       result.RewardID,
		PlayerID:       result.PlayerID,
		AlreadyClaimed: result.AlreadyClaimed,
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var syntaxErr *json.SyntaxError
		if errors.As(err, &syntaxErr) {
			return errors.New("malformed JSON")
		}
		if errors.Is(err, io.EOF) {
			return errors.New("request body is required")
		}
		if strings.Contains(err.Error(), "unknown field") {
			return errors.New("unknown field in request body")
		}
		return errors.New("invalid JSON body")
	}
	if dec.More() {
		return errors.New("request body must contain a single JSON object")
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{
			Error:   "internal_error",
			Message: "failed to encode response",
		})
		return
	}
	writeRawJSON(w, status, b)
}

func writeRawJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
