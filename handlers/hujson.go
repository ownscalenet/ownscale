package handlers

import (
	"net/http"

	"github.com/tailscale/hujson"
)

func (h *Handlers) FormatHuJSON(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}

	formatted, err := hujson.Format([]byte(req.Content))
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid HuJSON: "+err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"content": string(formatted)})
}
