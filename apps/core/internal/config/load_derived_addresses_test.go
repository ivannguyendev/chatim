package config_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

func TestLoadAdvertiseAddrFollowsCoreIDAndGRPCPort(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"default port", map[string]string{"CORE_ID": "core-b"}, "core-b:9000"},
		{"port of a bare grpc addr", map[string]string{"CORE_ID": "core-b", "CORE_GRPC_ADDR": ":7100"}, "core-b:7100"},
		{"port of a bound grpc addr", map[string]string{"CORE_ID": "core-b", "CORE_GRPC_ADDR": "0.0.0.0:7200"}, "core-b:7200"},
		{"explicit advertise addr wins", map[string]string{"CORE_ID": "core-b", "CORE_ADVERTISE_ADDR": "10.1.1.1:7300"}, "10.1.1.1:7300"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			got, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got.AdvertiseAddr != tt.want || got.Slot.Addr != tt.want {
				t.Errorf("AdvertiseAddr = %q, Slot.Addr = %q, want %q", got.AdvertiseAddr, got.Slot.Addr, tt.want)
			}
		})
	}
}

func TestLoadDerivesHookTimeoutFromTick(t *testing.T) {
	setEnv(t, map[string]string{"SLOT_TICK": "600ms"})
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Slot.HookTimeout != 300*time.Millisecond {
		t.Errorf("Slot.HookTimeout = %v, want half of SLOT_TICK", got.Slot.HookTimeout)
	}
}

func TestAdminAddrReadsOnlyTheAdminKey(t *testing.T) {
	for _, k := range envKeys {
		t.Setenv(k, "")
	}
	if got := config.AdminAddr(); got != ":9090" {
		t.Errorf("AdminAddr() = %q, want :9090", got)
	}
	t.Setenv("CORE_ADMIN_ADDR", "127.0.0.1:7091")
	if got := config.AdminAddr(); got != "127.0.0.1:7091" {
		t.Errorf("AdminAddr() = %q, want 127.0.0.1:7091", got)
	}
}
