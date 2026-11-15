package authz

import "testing"

func TestCanRespectsPermissions(t *testing.T) {
	if Can(nil, ActPlay) {
		t.Fatal("un anónimo no puede reproducir")
	}
	viewer := &Principal{Perms: Permissions{Play: true}}
	if !Can(viewer, ActPlay) || Can(viewer, ActDownload) || Can(viewer, ActDelete) {
		t.Fatal("permisos del espectador mal aplicados")
	}
	// Un administrador sin can_download tampoco descarga: los permisos de acción no se saltan por ser admin.
	admin := &Principal{IsAdmin: true, Perms: Permissions{Play: true}}
	if Can(admin, ActDownload) {
		t.Fatal("el administrador no debería saltarse can_download")
	}
}

func TestPolicyStrings(t *testing.T) {
	for _, p := range []Policy{Public, User, Owner, Admin} {
		if !p.Valid() || p.String() == "sin-politica" {
			t.Fatalf("política %d mal definida", p)
		}
	}
	if Policy(0).Valid() {
		t.Fatal("la política cero no debe ser válida")
	}
}
