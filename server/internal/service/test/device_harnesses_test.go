package test

import (
	"context"
	"testing"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	deviceservice "github.com/memory-co/agentuse/server/internal/service/device"
)

func TestHarnessIdentitiesComeOnlyFromDeviceReports(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	hs, err := s.DeviceService.ListHarnesses(ctx)
	if err != nil || len(hs) != 0 {
		t.Fatal("server seeded native adapters", hs, err)
	}
	u, _, _ := s.UserService.CreateUser(ctx, "u", "user", agentuse.RoleUser)
	d := registered(t, s, u, "device", "anything-local")
	if _, err = s.DeviceService.DeviceConnected(ctx, d, []deviceservice.ReportedHarness{{Name: "another-adapter", Version: "v42", Revision: "local-revision"}}); err != nil {
		t.Fatal(err)
	}
	hs, err = s.DeviceService.ListHarnesses(ctx)
	if err != nil || len(hs) != 1 || hs[0].Name != "another-adapter" {
		t.Fatal(hs, err)
	}
	got, _ := s.DeviceService.GetDevice(ctx, u, d.ID)
	found := false
	for _, h := range got.Harnesses {
		if h.Harness == "another-adapter" {
			found = h.HarnessRevision == "local-revision"
		}
	}
	if !found {
		t.Fatal("client revision was lost", got)
	}
}
