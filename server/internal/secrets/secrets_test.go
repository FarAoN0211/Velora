package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMasterKeyCreatedOnceWithStrictPermissions(t *testing.T) {
	dir := t.TempDir()
	k1, err := EnsureMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := EnsureMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != 32 || !bytes.Equal(k1, k2) {
		t.Fatal("la clave debe tener 32 bytes y no cambiar entre arranques")
	}
	st, err := os.Stat(filepath.Join(dir, "secrets", "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("permisos %o, esperaba 600", st.Mode().Perm())
	}
	d, _ := os.Stat(filepath.Join(dir, "secrets"))
	if d.Mode().Perm() != 0o700 {
		t.Fatalf("permisos del directorio %o, esperaba 700", d.Mode().Perm())
	}
}

func TestRejectsCorruptKey(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "secrets"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "secrets", "master.key"), []byte("corta"), 0o600)
	if _, err := EnsureMasterKey(dir); err == nil {
		t.Fatal("una clave de tamaño incorrecto debe rechazarse, nunca sustituirse en silencio")
	}
}
