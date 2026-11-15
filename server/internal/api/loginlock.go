package api

import (
	"context"
	"errors"
	"sync"
	"time"
)

// keyedLocks serializa los intentos de acceso por usuario y por IP. Es un cerrojo del proceso: VELORA tiene un único
// proceso servidor (el worker no atiende accesos). Un cerrojo de PostgreSQL retendría una conexión por petición en
// espera y agotaría el pool con muchas peticiones simultáneas. La espera es cancelable (cliente desconectado o plazo)
// y cada entrada del mapa se elimina cuando nadie la usa, también si la espera se cancela.
type keyedLocks struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	ch   chan struct{} // capacidad 1: ocupado = hay un valor dentro
	refs int
}

// loginLockWait es lo máximo que una petición espera su turno antes de responder «inténtalo de nuevo».
var loginLockWait = 10 * time.Second

var errLockTimeout = errors.New("demasiados intentos simultáneos")

func (k *keyedLocks) acquire(key string) *keyedLock {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.locks == nil {
		k.locks = map[string]*keyedLock{}
	}
	l := k.locks[key]
	if l == nil {
		l = &keyedLock{ch: make(chan struct{}, 1)}
		k.locks[key] = l
	}
	l.refs++
	return l
}

func (k *keyedLocks) release(key string, l *keyedLock) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if l.refs--; l.refs == 0 {
		delete(k.locks, key)
	}
}

// lock espera el cerrojo de key hasta que se obtiene, se cancela ctx o vence loginLockWait.
func (k *keyedLocks) lock(ctx context.Context, key string) (func(), error) {
	l := k.acquire(key)
	t := time.NewTimer(loginLockWait)
	defer t.Stop()
	select {
	case l.ch <- struct{}{}:
		return func() {
			<-l.ch
			k.release(key, l)
		}, nil
	case <-ctx.Done():
		k.release(key, l)
		return nil, ctx.Err()
	case <-t.C:
		k.release(key, l)
		return nil, errLockTimeout
	}
}

// with ejecuta fn con el cerrojo del usuario y el de la IP, siempre en ese orden (sin interbloqueos).
func (k *keyedLocks) with(ctx context.Context, username, ip string, fn func() error) error {
	unlockUser, err := k.lock(ctx, "u:"+username)
	if err != nil {
		return err
	}
	defer unlockUser()
	unlockIP, err := k.lock(ctx, "ip:"+ip)
	if err != nil {
		return err
	}
	defer unlockIP()
	return fn()
}

// size devuelve cuántas claves hay en el mapa (pruebas: no debe crecer con nombres inventados).
func (k *keyedLocks) size() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.locks)
}
