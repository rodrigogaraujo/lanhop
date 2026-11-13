package transport

import (
	"crypto/tls"
	"errors"
	"net"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	DefaultPort             = 24745
	defaultPingInterval     = time.Second
	DefaultSilenceLimit     = 3 * time.Second
	defaultHandshakeTimeout = 5 * time.Second
	pendingHandshakes       = 16
	defaultDataWindow       = 10 * time.Second
)

var (
	ErrUntrusted = errors.New("transport: the other machine is not paired with this one")
	ErrVersion   = errors.New("transport: the other machine speaks another protocol version")
	ErrNotLanhop = errors.New("transport: the other side did not answer as lanhop")
)

type Trusted interface {
	Find(fingerprint identity.Fingerprint) (trust.Peer, bool)
}

type Config struct {
	Identity         *identity.Identity
	PingInterval     time.Duration
	SilenceLimit     time.Duration
	HandshakeTimeout time.Duration
	DataWindow       time.Duration
	DataPace         *Pace
	OnReject         func(remote net.Addr, reason error)

	now                func() time.Time
	drawTransferNumber func() (int32, error)
}

func (c Config) withDefaults() Config {
	if c.PingInterval == 0 {
		c.PingInterval = defaultPingInterval
	}
	if c.SilenceLimit == 0 {
		c.SilenceLimit = DefaultSilenceLimit
	}
	if c.HandshakeTimeout == 0 {
		c.HandshakeTimeout = defaultHandshakeTimeout
	}
	if c.DataWindow == 0 {
		c.DataWindow = defaultDataWindow
	}
	if c.DataPace == nil {
		c.DataPace = paceOfThisMachine
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.drawTransferNumber == nil {
		c.drawTransferNumber = drawTransferNumber
	}
	return c
}

func fingerprintOfPeer(state tls.ConnectionState) (identity.Fingerprint, bool) {
	if len(state.PeerCertificates) == 0 {
		return identity.Fingerprint{}, false
	}
	return identity.FingerprintOf(state.PeerCertificates[0]), true
}

func acceptOnlyPaired(local *identity.Identity, trusted Trusted) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{local.Certificate},
		ClientAuth:   tls.RequireAnyClientCert,
		VerifyConnection: func(state tls.ConnectionState) error {
			fingerprint, presented := fingerprintOfPeer(state)
			if !presented {
				return ErrUntrusted
			}
			if _, paired := trusted.Find(fingerprint); !paired {
				return ErrUntrusted
			}
			return nil
		},
	}
}

func connectOnlyTo(local *identity.Identity, expected identity.Fingerprint) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{local.Certificate},
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if fingerprint, presented := fingerprintOfPeer(state); !presented || fingerprint != expected {
				return ErrUntrusted
			}
			return nil
		},
	}
}
