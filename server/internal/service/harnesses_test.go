package service

import (
	"context"
	"testing"
)

func TestHarnessIdentitiesComeOnlyFromDeviceReports(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	hs, err := s.ListHarnesses(ctx)
	if err != nil || len(hs) != 0 {
		t.Fatal("server seeded native adapters", hs, err)
	}
	u, _, _ := s.CreateUser(ctx, "u", "user", RoleUser)
	d := registered(t, s, u, "device", "anything-local")
	if _, err = s.DaemonConnected(ctx, d, []ReportedHarness{{Name: "another-adapter", Version: "v42", Revision: "local-revision"}}); err != nil {
		t.Fatal(err)
	}
	hs, err = s.ListHarnesses(ctx)
	if err != nil || len(hs) != 1 || hs[0].Name != "another-adapter" {
		t.Fatal(hs, err)
	}
	got, _ := s.GetDaemon(ctx, u, d.ID)
	found := false
	for _, h := range got.Harnesses {
		if h.Harness == "another-adapter" {
			found = h.Revision == "local-revision"
		}
	}
	if !found {
		t.Fatal("client revision was lost", got)
	}
}
