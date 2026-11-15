package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFake(t *testing.T, dir string, at time.Time, verified bool) string {
	t.Helper()
	name := "velora-" + at.Format("20060102-150405") + ".dump"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := writeMeta(filepath.Join(dir, name), Meta{File: name, CreatedAt: at, Verified: verified}); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestRotateKeepsDailyWeeklyAndLastVerified(t *testing.T) {
	dir := t.TempDir()
	s := &Service{Dir: dir, Keep: 3, KeepWeeks: 4}
	now := time.Now().UTC()
	var names []string
	for d := 0; d < 40; d++ { // 40 copias diarias; las 3 más recientes sin verificar
		names = append(names, writeFake(t, dir, now.AddDate(0, 0, -d), d >= 3))
	}
	if err := s.rotate(); err != nil {
		t.Fatal(err)
	}
	left, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	keep := map[string]bool{}
	for _, m := range left {
		keep[m.File] = true
	}
	for i := 0; i < 3; i++ {
		if !keep[names[i]] {
			t.Errorf("debería conservar la copia diaria %d", i)
		}
	}
	if !keep[names[3]] {
		t.Error("debería conservar la última copia verificada aunque no esté entre las diarias")
	}
	if keep[names[39]] {
		t.Error("la copia de hace 39 días queda fuera de las 4 semanas y debería borrarse")
	}
	if len(left) > 3+1+5 {
		t.Errorf("quedan demasiadas copias: %d", len(left))
	}
	for _, m := range left {
		if _, err := os.Stat(filepath.Join(dir, m.File)); err != nil {
			t.Errorf("falta el .dump de %s", m.File)
		}
	}
}
