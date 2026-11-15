package api

// E1.1 · Pruebas de aislamiento entre usuarios y de revocación (VELORA_DECISIONES_BIBLIOTECAS_PERMISOS.md §6 y revisión
// de ChatGPT): un usuario sin permiso no descubre ni reproduce nada por ninguna vía, y revocar corta lo que ya está abierto.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"velora/internal/library"
	"velora/internal/media"
	"velora/internal/store"
)

type e11Env struct {
	*e1Env
	pelis, anim         store.Library
	pelisItem, animItem uuid.UUID
	pelisFile, bigFile  uuid.UUID
	userB               uuid.UUID
	tokenB              string
}

func newE11Env(t *testing.T) *e11Env {
	t.Helper()
	e := &e11Env{e1Env: newE1Env(t, "")}
	ctx := context.Background()
	// Segunda biblioteca «Películas de animación»: otra película y un archivo grande (disperso) para probar la revocación.
	animDir := filepath.Join(e.mediaRoot, "Animacion", "Dibujos (2003)")
	if err := os.MkdirAll(animDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(filepath.Join(e.movieDir, "Prueba sintética (2020).mkv"))
	if err := os.WriteFile(filepath.Join(animDir, "Dibujos (2003).mkv"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	bigDir := filepath.Join(e.mediaRoot, "Animacion", "Larga (2010)")
	_ = os.MkdirAll(bigDir, 0o755)
	big, err := os.Create(filepath.Join(bigDir, "Larga (2010).mkv"))
	if err != nil {
		t.Fatal(err)
	}
	_ = big.Truncate(256 << 20)
	big.Close()

	var created struct {
		Library store.Library `json:"library"`
	}
	if c := e.call(t, "POST", "/api/v1/libraries", e.token, map[string]string{"name": "Películas", "path": filepath.Join(e.mediaRoot, "Peliculas")}, &created); c != 201 {
		t.Fatalf("crear Películas: %d", c)
	}
	e.pelis = created.Library
	if c := e.call(t, "POST", "/api/v1/libraries", e.token, map[string]any{"name": "Películas de animación", "kind": "movies", "path": filepath.Join(e.mediaRoot, "Animacion")}, &created); c != 201 {
		t.Fatalf("crear Animación: %d", c)
	}
	e.anim = created.Library
	e.scan(t, e.pelis.ID)
	e.scan(t, e.anim.ID)
	q := func(sql string, args ...any) (id uuid.UUID) {
		if err := e.pool.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return
	}
	e.pelisItem = q(`select id from media_items where library_id = $1`, e.pelis.ID)
	e.animItem = q(`select id from media_items where library_id = $1 and title = 'Dibujos'`, e.anim.ID)
	e.pelisFile = q(`select id from media_files where item_id = $1`, e.pelisItem)
	e.bigFile = q(`select f.id from media_files f join media_items i on i.id = f.item_id where i.title = 'Larga'`)

	if c := e.call(t, "POST", "/api/v1/admin/users", e.token, map[string]string{"username": "lucia", "display_name": "Lucía", "password": "otra-contraseña-larga"}, &struct {
		ID *uuid.UUID `json:"id"`
	}{&e.userB}); c != 201 {
		t.Fatalf("crear usuario: %d", c)
	}
	var sess struct {
		Token string `json:"token"`
	}
	if c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "lucia", "password": "otra-contraseña-larga"}, &sess); c != 200 {
		t.Fatalf("login de B: %d", c)
	}
	e.tokenB = sess.Token
	return e
}

func (e *e11Env) grantB(t *testing.T, libs ...uuid.UUID) {
	t.Helper()
	if libs == nil {
		libs = []uuid.UUID{}
	}
	if c := e.call(t, "PUT", "/api/v1/admin/users/"+e.userB.String()+"/libraries", e.token, map[string]any{"library_ids": libs}, nil); c != 204 {
		t.Fatalf("conceder bibliotecas: %d", c)
	}
}

// itemIDs devuelve los ids de /items (con búsqueda opcional) para el token dado.
func (e *e11Env) itemIDs(t *testing.T, token, query string) map[uuid.UUID]bool {
	t.Helper()
	var out struct {
		Items []store.ItemCard `json:"items"`
	}
	e.call(t, "GET", "/api/v1/items?q="+query, token, nil, &out)
	m := map[uuid.UUID]bool{}
	for _, i := range out.Items {
		m[i.ID] = true
	}
	return m
}

func TestNewUserAndNewLibraryGrantNothing(t *testing.T) {
	e := newE11Env(t)
	var libs struct {
		Libraries []map[string]any `json:"libraries"`
	}
	e.call(t, "GET", "/api/v1/libraries", e.tokenB, nil, &libs)
	if len(libs.Libraries) != 0 {
		t.Fatalf("un usuario nuevo no debe ver bibliotecas: %v", libs.Libraries)
	}
	if n := len(e.itemIDs(t, e.tokenB, "")); n != 0 {
		t.Fatalf("un usuario nuevo no debe ver películas: %d", n)
	}
	var home struct {
		Featured []store.Featured `json:"featured"`
		Continue []store.ItemCard `json:"continue"`
		Rows     []any            `json:"rows"`
	}
	e.call(t, "GET", "/api/v1/home", e.tokenB, nil, &home)
	if len(home.Rows)+len(home.Featured)+len(home.Continue) != 0 {
		t.Fatalf("inicio vacío para un usuario sin permisos: %+v", home)
	}
	// Con acceso a Animación, una biblioteca creada después no se le concede.
	e.grantB(t, e.anim.ID)
	newDir := filepath.Join(e.mediaRoot, "Nueva")
	_ = os.MkdirAll(newDir, 0o755)
	var created struct {
		Library store.Library `json:"library"`
	}
	e.call(t, "POST", "/api/v1/libraries", e.token, map[string]string{"name": "Nueva", "path": newDir}, &created)
	e.call(t, "GET", "/api/v1/libraries", e.tokenB, nil, &libs)
	if len(libs.Libraries) != 1 || libs.Libraries[0]["name"] != "Películas de animación" {
		t.Fatalf("B solo debe ver Animación: %v", libs.Libraries)
	}
	if _, leaked := libs.Libraries[0]["paths"]; leaked {
		t.Fatal("un usuario normal no debe ver las rutas del servidor")
	}
}

// Usuario B con acceso solo a Animación intenta llegar a una película de Películas por todas las vías.
func TestUserCannotReachOtherLibraryByAnyRoute(t *testing.T) {
	e := newE11Env(t)
	e.grantB(t, e.anim.ID)
	ids := e.itemIDs(t, e.tokenB, "")
	if ids[e.pelisItem] || !ids[e.animItem] {
		t.Fatalf("listado de B incorrecto: %v", ids)
	}
	if e.itemIDs(t, e.tokenB, "prueba")[e.pelisItem] {
		t.Fatal("la búsqueda descubre una película sin permiso")
	}
	item, file := e.pelisItem.String(), e.pelisFile.String()
	cases := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/items/" + item, nil},
		{"GET", "/api/v1/items/" + item + "/images/poster", nil},
		{"PUT", "/api/v1/me/items/" + item + "/progress", map[string]any{"position_ms": 1000, "duration_ms": 6000}},
		{"POST", "/api/v1/me/items/" + item + "/played", nil},
		{"POST", "/api/v1/me/items/" + item + "/continue-hidden", nil},
		{"POST", "/api/v1/playback/decide", map[string]any{"file_id": file, "caps": chromiumCaps}},
		{"POST", "/api/v1/playback/decide", map[string]any{"file_id": file, "caps": chromiumCaps, "dry_run": true}},
		{"GET", "/api/v1/stream/files/" + file, nil},
		{"GET", "/api/v1/stream/files/" + file + "/remux?audio=1&convert=true", nil},
	}
	for _, c := range cases {
		if code := e.call(t, c.method, c.path, e.tokenB, c.body, nil); code != 404 {
			t.Errorf("%s %s: B obtuvo %d, se esperaba 404", c.method, c.path, code)
		}
	}
	// Ni progreso ni marcas se han guardado para B.
	var n int
	_ = e.pool.QueryRow(context.Background(), `select count(*) from user_item_data where user_id = $1`, e.userB).Scan(&n)
	if n != 0 {
		t.Fatalf("se guardaron %d datos de B sobre contenido sin permiso", n)
	}
	// Las rutas de administración tampoco existen para B.
	for _, c := range []struct{ m, p string }{
		{"PATCH", "/api/v1/libraries/" + e.anim.ID.String()},
		{"PUT", "/api/v1/admin/users/" + e.userB.String() + "/libraries"},
		{"GET", "/api/v1/admin/users"},
		{"POST", "/api/v1/libraries/" + e.anim.ID.String() + "/paths"},
	} {
		if code := e.call(t, c.m, c.p, e.tokenB, map[string]any{}, nil); code != 404 {
			t.Errorf("%s %s: B obtuvo %d, se esperaba 404", c.m, c.p, code)
		}
	}
	// Revocar: el enlace obtenido antes deja de valer en la siguiente petición.
	if code := e.call(t, "GET", "/api/v1/items/"+e.animItem.String(), e.tokenB, nil, nil); code != 200 {
		t.Fatalf("con permiso debería ver la ficha: %d", code)
	}
	e.grantB(t)
	if code := e.call(t, "GET", "/api/v1/items/"+e.animItem.String(), e.tokenB, nil, nil); code != 404 {
		t.Fatalf("tras revocar, la ficha debería dar 404: %d", code)
	}
}

// Renombrar, ocultar del menú, reordenar y añadir rutas no cambia los permisos ni el contenido.
func TestLibraryChangesKeepPermissions(t *testing.T) {
	e := newE11Env(t)
	e.grantB(t, e.anim.ID)
	before := e.itemIDs(t, e.tokenB, "")
	if c := e.call(t, "PATCH", "/api/v1/libraries/"+e.anim.ID.String(), e.token, map[string]any{"name": "Cine de dibujos", "show_in_menu": false}, nil); c != 200 {
		t.Fatalf("renombrar/ocultar: %d", c)
	}
	if c := e.call(t, "PUT", "/api/v1/admin/library-order", e.token, map[string]any{"ids": []uuid.UUID{e.anim.ID, e.pelis.ID}}, nil); c != 204 {
		t.Fatalf("ordenar: %d", c)
	}
	extra := filepath.Join(e.mediaRoot, "MasAnimacion")
	_ = os.MkdirAll(extra, 0o755)
	if c := e.call(t, "POST", "/api/v1/libraries/"+e.anim.ID.String()+"/paths", e.token, map[string]string{"path": extra}, nil); c != 201 {
		t.Fatalf("añadir ruta: %d", c)
	}
	after := e.itemIDs(t, e.tokenB, "")
	if len(after) != len(before) || !after[e.animItem] || after[e.pelisItem] {
		t.Fatalf("los cambios de la biblioteca alteraron lo que ve B: %v → %v", before, after)
	}
	var libs struct {
		Libraries []map[string]any `json:"libraries"`
	}
	e.call(t, "GET", "/api/v1/libraries", e.tokenB, nil, &libs)
	if len(libs.Libraries) != 1 || libs.Libraries[0]["name"] != "Cine de dibujos" || libs.Libraries[0]["show_in_menu"] != false {
		t.Fatalf("B debería ver la biblioteca renombrada y oculta del menú (sigue teniendo acceso): %v", libs.Libraries)
	}
	var admin struct {
		Libraries []store.Library `json:"libraries"`
	}
	e.call(t, "GET", "/api/v1/libraries", e.token, nil, &admin)
	if admin.Libraries[0].ID != e.anim.ID || len(admin.Libraries[0].Paths) != 2 {
		t.Fatalf("orden o rutas incorrectos: %+v", admin.Libraries)
	}
	// La última ruta no se puede quitar.
	if c := e.call(t, "DELETE", "/api/v1/libraries/"+e.pelis.ID.String()+"/paths/"+e.pelis.Paths[0].ID.String(), e.token, nil, nil); c != 409 {
		t.Fatalf("quitar la última ruta debería dar 409: %d", c)
	}
}

// Una ruta remota que desaparece conserva fichas y progreso.
func TestUnavailablePathKeepsItemsAndProgress(t *testing.T) {
	e := newE11Env(t)
	e.grantB(t, e.anim.ID)
	if c := e.call(t, "PUT", "/api/v1/me/items/"+e.animItem.String()+"/progress", e.tokenB, map[string]any{"position_ms": 3000, "duration_ms": 6000}, nil); c != 204 {
		t.Fatalf("guardar progreso: %d", c)
	}
	dir := filepath.Join(e.mediaRoot, "Animacion")
	_ = os.Rename(dir, dir+".caido")
	_ = os.MkdirAll(dir, 0o755) // un montaje caído suele verse como carpeta vacía
	st := &store.Store{Pool: e.pool}
	lib, _ := st.LibraryByID(context.Background(), e.anim.ID)
	_, err := e.scanErr(lib)
	if err == nil {
		t.Fatal("el escaneo de una ruta vacía debería informar del problema")
	}
	var item struct {
		Item store.ItemDetail `json:"item"`
	}
	if c := e.call(t, "GET", "/api/v1/items/"+e.animItem.String(), e.tokenB, nil, &item); c != 200 || item.Item.PositionMS != 3000 {
		t.Fatalf("la ficha y el progreso deben conservarse: %d %d", c, item.Item.PositionMS)
	}
	var libs struct {
		Libraries []map[string]any `json:"libraries"`
	}
	e.call(t, "GET", "/api/v1/libraries", e.tokenB, nil, &libs)
	if libs.Libraries[0]["status"] != "unavailable" {
		t.Fatalf("la biblioteca debería figurar como inaccesible: %v", libs.Libraries[0]["status"])
	}
}

// Revocar el permiso mientras B reproduce (Direct Play de un archivo grande): el servidor corta el flujo en la siguiente
// comprobación periódica, sin depender del reproductor.
func TestRevokeCutsOpenStream(t *testing.T) {
	e := newE11Env(t)
	e.grantB(t, e.anim.ID)
	old := revokeCheckEvery
	revokeCheckEvery = 300 * time.Millisecond
	defer func() { revokeCheckEvery = old }()

	req, _ := http.NewRequest("GET", e.server.URL+"/api/v1/stream/files/"+e.bigFile.String(), nil)
	req.Header.Set("Authorization", "Bearer "+e.tokenB)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("B debería poder empezar a reproducir: %d", res.StatusCode)
	}
	first := make([]byte, 1<<20)
	if _, err := io.ReadFull(res.Body, first); err != nil {
		t.Fatal(err)
	}
	e.grantB(t) // se revoca todo
	// El reproductor sigue leyendo a su ritmo durante 1 s (más que el intervalo de comprobación) y después se vacía lo
	// que quede: lo ya enviado a los búferes del socket llega, pero el servidor no debe enviar nada más.
	deadline := time.Now().Add(time.Second)
	var total int64
	buf := make([]byte, 64<<10)
	for time.Now().Before(deadline) {
		n, err := res.Body.Read(buf)
		total += int64(n)
		if err != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rest, _ := io.Copy(io.Discard, res.Body)
	total += rest
	t.Logf("tras revocar llegaron %d MB (búferes en tránsito)", total>>20)
	if total+int64(len(first)) >= 256<<20 || total > 64<<20 {
		t.Fatalf("el flujo siguió tras revocar el permiso: %d MB", total>>20)
	}
	// Y una petición nueva (un salto) se rechaza al instante.
	req2, _ := http.NewRequest("GET", e.server.URL+"/api/v1/stream/files/"+e.bigFile.String(), nil)
	req2.Header.Set("Authorization", "Bearer "+e.tokenB)
	req2.Header.Set("Range", "bytes=1000-2000")
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != 404 {
		t.Fatalf("un salto tras revocar debería dar 404: %d", res2.StatusCode)
	}
}

func (e *e11Env) scanErr(lib store.Library) (any, error) {
	st := &store.Store{Pool: e.pool}
	return library.NewScanner(st, nil).ScanLibrary(context.Background(), lib)
}

// Quitar una carpeta no pierde nada: fichas, historial y progreso se recuperan; borrar de verdad exige confirmación.
func TestDetachedPathIsRecoverable(t *testing.T) {
	e := newE11Env(t)
	e.grantB(t, e.anim.ID)
	extra := filepath.Join(e.mediaRoot, "MasAnimacion")
	dir := filepath.Join(extra, "Extra (2011)")
	_ = os.MkdirAll(dir, 0o755)
	src, _ := os.ReadFile(filepath.Join(e.movieDir, "Prueba sintética (2020).mkv"))
	_ = os.WriteFile(filepath.Join(dir, "Extra (2011).mkv"), src, 0o644)
	var added struct {
		Path store.LibraryPath `json:"path"`
	}
	if c := e.call(t, "POST", "/api/v1/libraries/"+e.anim.ID.String()+"/paths", e.token, map[string]string{"path": extra}, &added); c != 201 {
		t.Fatalf("añadir carpeta: %d", c)
	}
	st := &store.Store{Pool: e.pool}
	lib, _ := st.LibraryByID(context.Background(), e.anim.ID)
	e.scanLib(t, lib)
	var extraItem uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `select id from media_items where title = 'Extra'`).Scan(&extraItem); err != nil {
		t.Fatal(err)
	}
	if c := e.call(t, "PUT", "/api/v1/me/items/"+extraItem.String()+"/progress", e.tokenB, map[string]any{"position_ms": 2000, "duration_ms": 6000}, nil); c != 204 {
		t.Fatalf("progreso: %d", c)
	}
	base := "/api/v1/libraries/" + e.anim.ID.String() + "/paths/" + added.Path.ID.String()
	if c := e.call(t, "DELETE", base, e.token, nil, nil); c != 204 {
		t.Fatalf("desvincular: %d", c)
	}
	if e.itemIDs(t, e.tokenB, "")[extraItem] || e.call(t, "GET", "/api/v1/items/"+extraItem.String(), e.tokenB, nil, nil) != 404 {
		t.Fatal("lo de una carpeta desvinculada no debe verse")
	}
	// Purgar sin confirmar no hace nada.
	if c := e.call(t, "POST", base+"/purge", e.token, map[string]string{}, nil); c != 422 {
		t.Fatalf("purgar sin confirmar debería dar 422: %d", c)
	}
	// Volver a añadir la misma carpeta la recupera con su contenido y el progreso de B.
	if c := e.call(t, "POST", "/api/v1/libraries/"+e.anim.ID.String()+"/paths", e.token, map[string]string{"path": extra}, nil); c != 201 {
		t.Fatalf("volver a añadir: %d", c)
	}
	var item struct {
		Item store.ItemDetail `json:"item"`
	}
	if c := e.call(t, "GET", "/api/v1/items/"+extraItem.String(), e.tokenB, nil, &item); c != 200 || item.Item.PositionMS != 2000 {
		t.Fatalf("la ficha y el progreso deberían recuperarse: %d %d", c, item.Item.PositionMS)
	}
	// Desvincular, recuperar por la ruta explícita y, por último, borrar definitivamente con confirmación.
	e.call(t, "DELETE", base, e.token, nil, nil)
	if c := e.call(t, "POST", base+"/restore", e.token, nil, nil); c != 204 || !e.itemIDs(t, e.tokenB, "")[extraItem] {
		t.Fatalf("recuperar: %d", c)
	}
	if c := e.call(t, "POST", base+"/purge", e.token, map[string]string{"confirm": "BORRAR"}, nil); c != 404 {
		t.Fatalf("no se puede purgar una carpeta activa: %d", c)
	}
	e.call(t, "DELETE", base, e.token, nil, nil)
	if c := e.call(t, "POST", base+"/purge", e.token, map[string]string{"confirm": "BORRAR"}, nil); c != 204 {
		t.Fatalf("purgar: %d", c)
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), `select count(*) from media_items where id = $1`, extraItem).Scan(&n)
	if n != 0 {
		t.Fatal("tras purgar con confirmación, la ficha debería haberse borrado")
	}
}

// Desactivar a un usuario corta también lo que está reproduciendo, no solo sus peticiones nuevas.
func TestDisableUserCutsOpenStream(t *testing.T) {
	e := newE11Env(t)
	e.grantB(t, e.anim.ID)
	old := revokeCheckEvery
	revokeCheckEvery = 300 * time.Millisecond
	defer func() { revokeCheckEvery = old }()
	req, _ := http.NewRequest("GET", e.server.URL+"/api/v1/stream/files/"+e.bigFile.String(), nil)
	req.Header.Set("Authorization", "Bearer "+e.tokenB)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	first := make([]byte, 1<<20)
	if _, err := io.ReadFull(res.Body, first); err != nil {
		t.Fatal(err)
	}
	if c := e.call(t, "PATCH", "/api/v1/admin/users/"+e.userB.String(), e.token, map[string]bool{"is_disabled": true}, nil); c != 204 {
		t.Fatalf("desactivar: %d", c)
	}
	deadline := time.Now().Add(time.Second)
	var total int64
	buf := make([]byte, 64<<10)
	for time.Now().Before(deadline) {
		n, err := res.Body.Read(buf)
		total += int64(n)
		if err != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rest, _ := io.Copy(io.Discard, res.Body)
	total += rest
	t.Logf("tras desactivar llegaron %d MB (búferes en tránsito)", total>>20)
	if total > 64<<20 {
		t.Fatalf("el flujo siguió tras desactivar al usuario: %d MB", total>>20)
	}
	if c := e.call(t, "GET", "/api/v1/me", e.tokenB, nil, nil); c != 401 {
		t.Fatalf("la sesión de un usuario desactivado no debe valer: %d", c)
	}
}

func (e *e11Env) scanLib(t *testing.T, lib store.Library) {
	t.Helper()
	if _, err := e.scanErr(lib); err != nil {
		t.Fatal(err)
	}
}

// Retirar una biblioteca entera tampoco pierde nada; al recuperarla vuelve con sus permisos pero oculta del menú.
func TestRetiredLibraryIsRecoverable(t *testing.T) {
	e := newE11Env(t)
	e.grantB(t, e.anim.ID)
	if c := e.call(t, "PUT", "/api/v1/me/items/"+e.animItem.String()+"/progress", e.tokenB, map[string]any{"position_ms": 4000, "duration_ms": 6000}, nil); c != 204 {
		t.Fatalf("progreso: %d", c)
	}
	base := "/api/v1/libraries/" + e.anim.ID.String()
	if c := e.call(t, "DELETE", base, e.token, nil, nil); c != 204 {
		t.Fatalf("retirar: %d", c)
	}
	var libs struct {
		Libraries []map[string]any `json:"libraries"`
	}
	e.call(t, "GET", "/api/v1/libraries", e.tokenB, nil, &libs)
	if len(libs.Libraries) != 0 || e.call(t, "GET", "/api/v1/items/"+e.animItem.String(), e.tokenB, nil, nil) != 404 ||
		e.call(t, "GET", "/api/v1/items/"+e.animItem.String(), e.token, nil, nil) != 404 {
		t.Fatal("una biblioteca retirada no debe verse (tampoco su contenido para el administrador)")
	}
	if c := e.call(t, "POST", base+"/purge", e.token, map[string]string{}, nil); c != 422 {
		t.Fatalf("purgar sin confirmar debería dar 422: %d", c)
	}
	if c := e.call(t, "POST", base+"/restore", e.token, nil, nil); c != 204 {
		t.Fatalf("recuperar: %d", c)
	}
	e.call(t, "GET", "/api/v1/libraries", e.tokenB, nil, &libs)
	if len(libs.Libraries) != 1 || libs.Libraries[0]["show_in_menu"] != false {
		t.Fatalf("debe volver con sus permisos y oculta del menú: %v", libs.Libraries)
	}
	var item struct {
		Item store.ItemDetail `json:"item"`
	}
	if c := e.call(t, "GET", "/api/v1/items/"+e.animItem.String(), e.tokenB, nil, &item); c != 200 || item.Item.PositionMS != 4000 {
		t.Fatalf("ficha y progreso deben recuperarse: %d %d", c, item.Item.PositionMS)
	}
	if c := e.call(t, "POST", base+"/purge", e.token, map[string]string{"confirm": "BORRAR"}, nil); c != 404 {
		t.Fatalf("no se puede purgar una biblioteca activa: %d", c)
	}
	e.call(t, "DELETE", base, e.token, nil, nil)
	if c := e.call(t, "POST", base+"/purge", e.token, map[string]string{"confirm": "BORRAR"}, nil); c != 204 {
		t.Fatalf("purgar: %d", c)
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), `select count(*) from media_items where library_id = $1`, e.anim.ID).Scan(&n)
	if n != 0 {
		t.Fatal("tras purgar con confirmación, las fichas deberían haberse borrado")
	}
}

// B1 (revisión de código): ninguna respuesta accesible a un usuario normal contiene rutas del servidor ni nombres de
// archivo (ni en los datos ni en los errores).
func TestNoServerPathsReachNormalUsers(t *testing.T) {
	e := newE11Env(t)
	e.grantB(t, e.anim.ID)
	animFile := ""
	_ = e.pool.QueryRow(context.Background(), `select id::text from media_files where item_id = $1`, e.animItem).Scan(&animFile)
	type req struct {
		method, path string
		body         any
	}
	reqs := []req{
		{"GET", "/api/v1/libraries", nil},
		{"GET", "/api/v1/home", nil},
		{"GET", "/api/v1/items", nil},
		{"GET", "/api/v1/items?q=dibujos", nil},
		{"GET", "/api/v1/items/" + e.animItem.String(), nil},
		{"GET", "/api/v1/items/" + e.pelisItem.String(), nil},
		{"POST", "/api/v1/playback/decide", map[string]any{"file_id": animFile, "caps": chromiumCaps}},
		{"POST", "/api/v1/playback/decide", map[string]any{"file_id": e.pelisFile.String(), "caps": chromiumCaps}},
		{"GET", "/api/v1/stream/files/" + e.pelisFile.String(), nil},
		{"GET", "/api/v1/me", nil},
	}
	for _, r := range reqs {
		var raw []byte
		if r.body != nil {
			raw, _ = json.Marshal(r.body)
		}
		hr, _ := http.NewRequest(r.method, e.server.URL+r.path, bytes.NewReader(raw))
		hr.Header.Set("Authorization", "Bearer "+e.tokenB)
		hr.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(hr)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		for _, leak := range []string{e.mediaRoot, "Animacion/", ".mkv", "/media"} {
			if strings.Contains(string(body), leak) {
				t.Errorf("%s %s expone %q: %s", r.method, r.path, leak, body)
			}
		}
	}
}

// B2 (revisión de código): dos desvinculaciones simultáneas no dejan una biblioteca sin rutas activas.
func TestConcurrentDetachKeepsOnePath(t *testing.T) {
	e := newE11Env(t)
	extra := filepath.Join(e.mediaRoot, "MasAnimacion")
	_ = os.MkdirAll(extra, 0o755)
	if c := e.call(t, "POST", "/api/v1/libraries/"+e.anim.ID.String()+"/paths", e.token, map[string]string{"path": extra}, nil); c != 201 {
		t.Fatalf("añadir carpeta: %d", c)
	}
	st := &store.Store{Pool: e.pool}
	lib, _ := st.LibraryByID(context.Background(), e.anim.ID)
	if len(lib.Paths) != 2 {
		t.Fatalf("se esperaban 2 rutas activas: %d", len(lib.Paths))
	}
	codes := make(chan int, 2)
	start := make(chan struct{})
	for _, p := range lib.Paths {
		go func(id uuid.UUID) {
			<-start
			codes <- e.call(t, "DELETE", "/api/v1/libraries/"+e.anim.ID.String()+"/paths/"+id.String(), e.token, nil, nil)
		}(p.ID)
	}
	close(start)
	a, b := <-codes, <-codes
	lib, _ = st.LibraryByID(context.Background(), e.anim.ID)
	if len(lib.Paths) != 1 || !((a == 204 && b == 409) || (a == 409 && b == 204)) {
		t.Fatalf("debe quedar exactamente una ruta activa: %d rutas, respuestas %d y %d", len(lib.Paths), a, b)
	}
}

// B3 (revisión de código): una carpeta quitada cuyo tipo de montaje ha cambiado no se recupera a ciegas.
func TestRestorePathRejectsChangedMountKind(t *testing.T) {
	e := newE11Env(t)
	extra := filepath.Join(e.mediaRoot, "MasAnimacion")
	_ = os.MkdirAll(extra, 0o755)
	var added struct {
		Path store.LibraryPath `json:"path"`
	}
	e.call(t, "POST", "/api/v1/libraries/"+e.anim.ID.String()+"/paths", e.token, map[string]string{"path": extra}, &added)
	base := "/api/v1/libraries/" + e.anim.ID.String() + "/paths/" + added.Path.ID.String()
	if c := e.call(t, "DELETE", base, e.token, nil, nil); c != 204 {
		t.Fatalf("desvincular: %d", c)
	}
	// Se simula que cuando se quitó era un montaje remoto y ahora la carpeta es local.
	if _, err := e.pool.Exec(context.Background(), `update library_paths set kind = 'remote_fuse' where id = $1`, added.Path.ID); err != nil {
		t.Fatal(err)
	}
	if c := e.call(t, "POST", base+"/restore", e.token, nil, nil); c != 409 {
		t.Fatalf("recuperar con otro tipo de montaje debería dar 409: %d", c)
	}
	if c := e.call(t, "POST", "/api/v1/libraries/"+e.anim.ID.String()+"/paths", e.token, map[string]string{"path": extra}, nil); c != 409 {
		t.Fatalf("volver a añadirla con otro tipo de montaje debería dar 409: %d", c)
	}
	// Una carpeta que ya no existe tampoco se recupera.
	if _, err := e.pool.Exec(context.Background(), `update library_paths set kind = 'local' where id = $1`, added.Path.ID); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(extra)
	if c := e.call(t, "POST", base+"/restore", e.token, nil, nil); c != 409 {
		t.Fatalf("recuperar una carpeta que ya no existe debería dar 409: %d", c)
	}
}

// R4 (revisión de código): una posición NaN o infinita se rechaza antes de llegar a FFmpeg.
func TestRemuxRejectsNonFiniteStart(t *testing.T) {
	e := newE11Env(t)
	base := "/api/v1/stream/files/" + e.pelisFile.String() + "/remux?audio=1&convert=true&t="
	for _, v := range []string{"NaN", "nan", "%2BInf", "-Inf", "1e400", "abc"} {
		res := e.raw(t, "GET", base+v, nil)
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Errorf("t=%s debería dar 400, dio %d", v, res.StatusCode)
		}
	}
	for _, v := range []string{"-5", "99999", "2.5"} {
		res := e.raw(t, "GET", base+v, nil)
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Errorf("t=%s debería aceptarse (fuera de rango = desde el principio), dio %d", v, res.StatusCode)
		}
	}
}

// R5 (revisión de código): retirar una biblioteca mientras se escanea no la hace visible, no pierde progreso y no marca
// ausencias indebidas.
func TestRetireDuringScan(t *testing.T) {
	e := newE11Env(t)
	e.grantB(t, e.anim.ID)
	if c := e.call(t, "PUT", "/api/v1/me/items/"+e.animItem.String()+"/progress", e.tokenB, map[string]any{"position_ms": 1500, "duration_ms": 6000}, nil); c != 204 {
		t.Fatalf("progreso: %d", c)
	}
	// Una película nueva hace que el escaneo tenga que analizar algo; el análisis se queda esperando.
	dir := filepath.Join(e.mediaRoot, "Animacion", "Nueva (2024)")
	_ = os.MkdirAll(dir, 0o755)
	src, _ := os.ReadFile(filepath.Join(e.movieDir, "Prueba sintética (2020).mkv"))
	_ = os.WriteFile(filepath.Join(dir, "Nueva (2024).mkv"), src, 0o644)
	st := &store.Store{Pool: e.pool}
	lib, _ := st.LibraryByID(context.Background(), e.anim.ID)
	sc := library.NewScanner(st, nil)
	probing, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	sc.Probe = func(ctx context.Context, f *os.File) (media.Probe, error) {
		once.Do(func() { close(probing) })
		<-release
		return media.RunProbe(ctx, f)
	}
	done := make(chan error, 1)
	go func() { _, err := sc.ScanLibrary(context.Background(), lib); done <- err }()
	<-probing
	if c := e.call(t, "DELETE", "/api/v1/libraries/"+e.anim.ID.String(), e.token, nil, nil); c != 204 {
		t.Fatalf("retirar: %d", c)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("el escaneo en curso falló: %v", err)
	}
	if n := len(e.itemIDs(t, e.tokenB, "")); n != 0 || len(e.itemIDs(t, e.token, "")) != 1 {
		t.Fatal("el escaneo no debe volver visible una biblioteca retirada")
	}
	var missing, progress int
	_ = e.pool.QueryRow(context.Background(), `select count(*) from media_files where missing_since is not null`).Scan(&missing)
	_ = e.pool.QueryRow(context.Background(), `select position_ms from user_item_data where item_id = $1`, e.animItem).Scan(&progress)
	if missing != 0 || progress != 1500 {
		t.Fatalf("no debe haber ausencias (%d) y el progreso debe seguir (%d)", missing, progress)
	}
	if c := e.call(t, "POST", "/api/v1/libraries/"+e.anim.ID.String()+"/restore", e.token, nil, nil); c != 204 {
		t.Fatalf("recuperar: %d", c)
	}
	if n := len(e.itemIDs(t, e.tokenB, "")); n != 3 {
		t.Fatalf("tras recuperarla deben verse sus 3 películas, incluida la escaneada durante la retirada: %d", n)
	}
}

// R3 (revisión de código): en Direct Stream, si el cliente deja de leer (la escritura se bloquea y la comprobación
// periódica no llega a ejecutarse), el corte por inactividad libera FFmpeg y su hueco igualmente. Con un vídeo cuya salida
// no cabe en los búferes del socket.
func TestRemuxBlockedClientReleasesSlot(t *testing.T) {
	e := newE11Env(t)
	e.grantB(t, e.anim.ID)
	big := filepath.Join(e.mediaRoot, "Animacion", "Pesada (2015)")
	_ = os.MkdirAll(big, 0o755)
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=25:duration=40",
		"-f", "lavfi", "-i", "sine=duration=40", "-c:v", "libx264", "-preset", "ultrafast", "-b:v", "8M", "-g", "50", "-c:a", "ac3",
		filepath.Join(big, "Pesada (2015).mkv"))
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	st := &store.Store{Pool: e.pool}
	lib, _ := st.LibraryByID(context.Background(), e.anim.ID)
	if _, err := library.NewScanner(st, nil).ScanLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	var file string
	_ = e.pool.QueryRow(context.Background(), `select f.id::text from media_files f join media_items i on i.id = f.item_id where i.title = 'Pesada'`).Scan(&file)
	oldIdle, oldCheck := remuxIdle, revokeCheckEvery
	remuxIdle, revokeCheckEvery = time.Second, 300*time.Millisecond
	defer func() { remuxIdle, revokeCheckEvery = oldIdle, oldCheck }()
	req, _ := http.NewRequest("GET", e.server.URL+"/api/v1/stream/files/"+file+"/remux?audio=1&convert=true", nil)
	req.Header.Set("Authorization", "Bearer "+e.tokenB)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if _, err := io.ReadFull(res.Body, make([]byte, 64<<10)); err != nil {
		t.Fatal(err)
	}
	if e.srv.remux.InUse() != 1 {
		t.Fatalf("debería haber un remux en curso: %d", e.srv.remux.InUse())
	}
	e.grantB(t) // se revoca y el cliente deja de leer
	deadline := time.Now().Add(15 * time.Second)
	for e.srv.remux.InUse() != 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if n := e.srv.remux.InUse(); n != 0 {
		t.Fatalf("el hueco de FFmpeg no se liberó con el cliente bloqueado: %d en uso", n)
	}
}

// Cada usuario decide qué bibliotecas ve en su barra lateral; no afecta a otros usuarios ni al acceso.
func TestUserMenuPreference(t *testing.T) {
	e := newE11Env(t)
	e.grantB(t, e.anim.ID)
	inMenu := func(token string, id uuid.UUID) bool {
		var libs struct {
			Libraries []struct {
				ID     uuid.UUID `json:"id"`
				InMenu bool      `json:"in_menu"`
			} `json:"libraries"`
		}
		e.call(t, "GET", "/api/v1/libraries", token, nil, &libs)
		for _, l := range libs.Libraries {
			if l.ID == id {
				return l.InMenu
			}
		}
		t.Fatalf("biblioteca %s no listada", id)
		return false
	}
	if !inMenu(e.tokenB, e.anim.ID) {
		t.Fatal("por defecto, lo que diga el administrador (visible)")
	}
	if c := e.call(t, "PUT", "/api/v1/me/libraries/"+e.anim.ID.String()+"/menu", e.tokenB, map[string]bool{"visible": false}, nil); c != 204 {
		t.Fatalf("ocultar: %d", c)
	}
	if inMenu(e.tokenB, e.anim.ID) || !inMenu(e.token, e.anim.ID) {
		t.Fatal("ocultarla para B no debe afectar al administrador")
	}
	if !e.itemIDs(t, e.tokenB, "")[e.animItem] {
		t.Fatal("ocultarla del menú no quita el acceso")
	}
	// Una biblioteca sin acceso no se puede tocar (404).
	if c := e.call(t, "PUT", "/api/v1/me/libraries/"+e.pelis.ID.String()+"/menu", e.tokenB, map[string]bool{"visible": true}, nil); c != 404 {
		t.Fatalf("sin acceso debería dar 404: %d", c)
	}
	// Puede mostrar una que el administrador ocultó del menú.
	e.call(t, "PATCH", "/api/v1/libraries/"+e.anim.ID.String(), e.token, map[string]any{"show_in_menu": false}, nil)
	e.call(t, "PUT", "/api/v1/me/libraries/"+e.anim.ID.String()+"/menu", e.tokenB, map[string]bool{"visible": true}, nil)
	if !inMenu(e.tokenB, e.anim.ID) {
		t.Fatal("la preferencia del usuario manda en su propio menú")
	}
}
