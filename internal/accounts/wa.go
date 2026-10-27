package accounts

import (
	"context"

	"go.mau.fi/whatsmeow"

	"github.com/datahase/whatsapp-mcp/internal/client"
)

type qrItem = whatsmeow.QRChannelItem

// realWA adapts *client.Client to the lifecycle interface.
type realWA struct{ *client.Client }

func (r realWA) Real() live { return r.Client }

func (r realWA) QRChannel(ctx context.Context) (<-chan qrItem, error) {
	return r.Client.QRChannel(ctx)
}
