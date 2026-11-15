package api

import (
	"net/http"
	"strconv"
)

func (s *Server) catalogRevision(w http.ResponseWriter, r *http.Request) {
	var rev int64
	if err := s.store.Pool.QueryRow(r.Context(), `select revision from catalog_revision where singleton`).Scan(&rev); err != nil {
		s.internal(w, "catalog/revision", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]string{"revision": strconv.FormatInt(rev, 10)})
}
