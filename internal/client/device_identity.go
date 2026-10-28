package client

import (
	wmstore "go.mau.fi/whatsmeow/store"
	"google.golang.org/protobuf/proto"
)

func init() {
	// This name is serialized into the companion registration payload used
	// during pairing. Set the library-global value before any accounts can
	// connect; changing it while clients are pairing would race.
	wmstore.DeviceProps.Os = proto.String("WhatsApp MCP")
}
