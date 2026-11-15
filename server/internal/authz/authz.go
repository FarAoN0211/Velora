// Package authz define las políticas de acceso y el registro de rutas (architecture.md §8).
//
// Regla: toda ruta se registra con una política explícita. Una ruta sin política no puede registrarse
// (pánico al arrancar) y un test recorre el router para comprobar que no existe ninguna ruta registrada por otra vía.
package authz

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/google/uuid"
)

// Policy es el nivel de acceso de una ruta.
type Policy int

const (
	unset Policy = iota
	// Public no requiere sesión.
	Public
	// User requiere una sesión válida de un usuario habilitado.
	User
	// Owner requiere sesión; además, el handler comprueba que el recurso pertenece al usuario (o es admin).
	Owner
	// Admin requiere una sesión de administrador.
	Admin
)

func (p Policy) String() string {
	switch p {
	case Public:
		return "public"
	case User:
		return "user"
	case Owner:
		return "owner"
	case Admin:
		return "admin"
	}
	return "sin-politica"
}

// Valid indica si la política es una de las definidas.
func (p Policy) Valid() bool { return p >= Public && p <= Admin }

// Principal es el usuario autenticado de la petición.
type Principal struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	Username  string
	IsAdmin   bool
	ViaCookie bool // si la sesión llegó por cookie, las peticiones que cambian algo exigen token anti-CSRF
	Perms     Permissions
	// Locked: perfil con PIN bloqueado por falta de uso; solo puede desbloquearse, cambiar de perfil o salir.
	Locked bool
}

// Permissions son los permisos de acción del usuario, cargados con la sesión en cada petición (E0.1, punto 7).
// La política de la ruta solo dice quién puede llamar; estos permisos deciden qué puede hacer. Desde E1, además,
// toda consulta de catálogo y todo acceso a ficheros pasa por el predicado de visibilidad (architecture.md §6).
type Permissions struct {
	Play     bool
	Download bool
	Delete   bool
	Remote   bool
}

// Action es una acción sujeta a permiso.
type Action int

const (
	ActPlay Action = iota + 1
	ActDownload
	ActDelete
)

// Can indica si el principal puede realizar la acción. Los administradores no se saltan can_play/can_download:
// esos permisos también pueden limitarse en una cuenta de administración.
func Can(who *Principal, a Action) bool {
	if who == nil {
		return false
	}
	switch a {
	case ActPlay:
		return who.Perms.Play
	case ActDownload:
		return who.Perms.Download
	case ActDelete:
		return who.Perms.Delete
	}
	return false
}

type ctxKey struct{}

// WithPrincipal guarda el principal en el contexto.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext devuelve el principal o nil si la petición es anónima.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}

// Allows decide si el principal cumple la política (la parte de "dueño del recurso" la completa el handler).
func Allows(p Policy, who *Principal) bool {
	switch p {
	case Public:
		return true
	case User, Owner:
		return who != nil
	case Admin:
		return who != nil && who.IsAdmin
	}
	return false
}

// CanAccessOwned es la comprobación de recurso para las rutas Owner: el dueño o un administrador.
func CanAccessOwned(who *Principal, owner uuid.UUID) bool {
	return who != nil && (who.IsAdmin || who.UserID == owner)
}

// Route describe una ruta registrada.
type Route struct {
	Method  string
	Pattern string
	Policy  Policy
}

// Registry guarda todas las rutas con su política.
type Registry struct {
	mu     sync.Mutex
	routes map[string]Route
}

// NewRegistry crea un registro vacío.
func NewRegistry() *Registry { return &Registry{routes: map[string]Route{}} }

// Add registra una ruta; entra en pánico si la política falta o la ruta está duplicada.
func (r *Registry) Add(method, pattern string, p Policy) {
	if !p.Valid() {
		panic(fmt.Sprintf("ruta %s %s registrada sin política de acceso", method, pattern))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := method + " " + pattern
	if _, dup := r.routes[key]; dup {
		panic("ruta duplicada: " + key)
	}
	r.routes[key] = Route{Method: method, Pattern: pattern, Policy: p}
}

// Lookup devuelve la ruta registrada, si existe.
func (r *Registry) Lookup(method, pattern string) (Route, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.routes[method+" "+pattern]
	return rt, ok
}

// All devuelve las rutas ordenadas (para tests y documentación).
func (r *Registry) All() []Route {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Route, 0, len(r.routes))
	for _, rt := range r.routes {
		out = append(out, rt)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pattern == out[j].Pattern {
			return out[i].Method < out[j].Method
		}
		return out[i].Pattern < out[j].Pattern
	})
	return out
}
