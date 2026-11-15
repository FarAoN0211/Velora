package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("una-contraseña-larga")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("formato inesperado: %s", h)
	}
	if err := VerifyPassword(h, "una-contraseña-larga"); err != nil {
		t.Fatalf("debería verificar: %v", err)
	}
	if err := VerifyPassword(h, "otra"); err != ErrMismatch {
		t.Fatalf("esperaba ErrMismatch, obtuve %v", err)
	}
}

func TestSaltsDiffer(t *testing.T) {
	a, _ := HashPassword("misma")
	b, _ := HashPassword("misma")
	if a == b {
		t.Fatal("dos hashes de la misma contraseña no deben coincidir")
	}
}

func TestRejectsMalformed(t *testing.T) {
	for _, h := range []string{"", "texto", "$bcrypt$x$y$z$w", "$argon2id$v=19$m=1,t=1,p=1$!!$!!"} {
		if VerifyPassword(h, "x") == nil {
			t.Fatalf("hash malformado aceptado: %q", h)
		}
	}
}

func TestToken(t *testing.T) {
	tok, h, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) < 43 {
		t.Fatalf("token demasiado corto: %d", len(tok))
	}
	if string(HashToken(tok)) != string(h) {
		t.Fatal("el hash del token no es estable")
	}
}
