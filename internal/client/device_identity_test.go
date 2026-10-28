package client

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func TestPairingPayloadUsesProductDeviceName(t *testing.T) {
	// Exercise the actual registration serialization, without connecting,
	// saving credentials, or modifying the library's shared defaults in a test.
	device := (&sqlstore.Container{}).NewDevice()
	registration := device.GetClientPayload().GetDevicePairingData()
	props := &waCompanionReg.DeviceProps{}
	if err := proto.Unmarshal(registration.GetDeviceProps(), props); err != nil {
		t.Fatal(err)
	}
	if props.GetOs() != "WhatsApp MCP" {
		t.Fatalf("pairing advertised device name %q", props.GetOs())
	}

	// Reconnecting an existing identity must remain a login, not a new pair.
	id := types.NewJID("1234567890", types.DefaultUserServer)
	device.ID = &id
	if device.GetClientPayload().GetDevicePairingData() != nil {
		t.Fatal("existing session unexpectedly sent pairing registration")
	}
}
