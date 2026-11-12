package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

func Dial(ctx context.Context, config Config, peer trust.Peer) (*Conn, error) {
	config = config.withDefaults()
	deadline := time.Now().Add(config.HandshakeTimeout)
	untilDeadline, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	dialer := &tls.Dialer{NetDialer: &net.Dialer{}, Config: connectOnlyTo(config.Identity, peer.Fingerprint)}
	raw, err := dialer.DialContext(untilDeadline, "tcp", peer.Address)
	if err != nil {
		return nil, fmt.Errorf("transport: cannot connect to %s at %s: %w", peer.Name, peer.Address, err)
	}
	secured := raw.(*tls.Conn)
	stopWatching := context.AfterFunc(ctx, func() { secured.Close() })
	defer stopWatching()

	if err := greet(secured, deadline); err != nil {
		secured.Close()
		return nil, fmt.Errorf("transport: %s at %s refused the connection: %w", peer.Name, peer.Address, err)
	}
	return newConn(secured, peer, config), nil
}

func greet(secured *tls.Conn, deadline time.Time) error {
	if err := secured.SetDeadline(deadline); err != nil {
		return err
	}
	if err := exchangeHello(secured); err != nil {
		return err
	}
	return secured.SetDeadline(time.Time{})
}
