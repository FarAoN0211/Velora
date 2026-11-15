package api

import "testing"

// El orden elegido en una biblioteca se recuerda por usuario (web y TV lo leen de /api/v1/libraries al entrar).
func TestLibraryViewRemembered(t *testing.T) {
	e := newE11Env(t)
	e.grantB(t, e.pelis.ID)
	type lib struct {
		ID          string `json:"id"`
		ViewSort    string `json:"view_sort"`
		ViewReverse bool   `json:"view_reverse"`
	}
	views := func(token string) map[string]lib {
		var r struct {
			Libraries []lib `json:"libraries"`
		}
		if c := e.call(t, "GET", "/api/v1/libraries", token, nil, &r); c != 200 {
			t.Fatalf("bibliotecas: %d", c)
		}
		out := map[string]lib{}
		for _, l := range r.Libraries {
			out[l.ID] = l
		}
		return out
	}
	pelis := e.pelis.ID.String()
	if v := views(e.tokenB)[pelis]; v.ViewSort != "title" || v.ViewReverse {
		t.Fatalf("sin preferencia, título A→Z: %+v", v)
	}
	base := "/api/v1/me/libraries/" + pelis + "/view"
	if c := e.call(t, "PUT", base, e.tokenB, map[string]any{"sort": "year", "reverse": true}, nil); c != 204 {
		t.Fatalf("guardar orden: %d", c)
	}
	if v := views(e.tokenB)[pelis]; v.ViewSort != "year" || !v.ViewReverse {
		t.Fatalf("orden recordado: %+v", v)
	}
	// Es de cada usuario: el administrador sigue con el suyo.
	if v := views(e.token)[pelis]; v.ViewSort != "title" {
		t.Fatalf("el orden de otro usuario no cambia: %+v", v)
	}
	if c := e.call(t, "PUT", base, e.tokenB, map[string]any{"sort": "inventado"}, nil); c != 422 {
		t.Fatalf("orden no válido debería dar 422: %d", c)
	}
	// Sin acceso a la biblioteca: 404, como el resto del catálogo.
	if c := e.call(t, "PUT", "/api/v1/me/libraries/"+e.anim.ID.String()+"/view", e.tokenB, map[string]any{"sort": "year"}, nil); c != 404 {
		t.Fatalf("sin acceso debería dar 404: %d", c)
	}
	if c := e.call(t, "PUT", base, e.tokenB, map[string]any{"sort": "added", "reverse": false}, nil); c != 204 {
		t.Fatalf("cambiar orden: %d", c)
	}
	if v := views(e.tokenB)[pelis]; v.ViewSort != "added" || v.ViewReverse {
		t.Fatalf("último orden elegido: %+v", v)
	}
}
