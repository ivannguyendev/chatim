package command_test

import "testing"

func TestDevicesCannotCallInternalOnlyCoreCommands(t *testing.T) {
	internal := map[string]bool{"ListMyRoomIDs": true}
	for _, field := range requestFields() {
		if name := commandName(field); internal[name] {
			t.Fatalf("client request %s reaches the internal-only core command %s", field.Name(), name)
		}
	}
}
