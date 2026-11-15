package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const maxBody = 1 << 20 // 1 MiB para peticiones JSON

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError responde con un error legible en castellano y un código estable para los clientes.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: message}})
}

// readJSON decodifica el cuerpo con límite de tamaño y sin campos desconocidos.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			writeError(w, http.StatusRequestEntityTooLarge, "demasiado_grande", "La petición es demasiado grande.")
		case errors.Is(err, io.EOF):
			writeError(w, http.StatusBadRequest, "cuerpo_vacio", "Falta el cuerpo de la petición.")
		default:
			writeError(w, http.StatusBadRequest, "json_no_valido", "La petición no tiene un formato válido.")
		}
		return false
	}
	return true
}
