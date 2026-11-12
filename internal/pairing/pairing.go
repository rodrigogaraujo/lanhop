package pairing

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	DefaultPort    = transport.DefaultPort + 1
	defaultTimeout = 2 * time.Minute
	fallbackName   = "machine"
)

var (
	ErrDeclined      = errors.New("pairing: refused on this machine")
	ErrPeerDeclined  = errors.New("pairing: the other machine refused")
	ErrTimeout       = errors.New("pairing: nobody answered in time")
	ErrMalformed     = errors.New("pairing: the other side sent a message that is not a pairing message")
	ErrAlreadyPaired = errors.New("pairing: the other machine is already trusted")
	ErrNameTaken     = errors.New("pairing: another trusted machine already has this name")
	ErrSelf          = errors.New("pairing: the other machine presented the key of this machine")
)

type Question struct {
	Name      string
	OwnCode   string
	OtherCode string
}

type Asker interface {
	Confirm(question Question) bool
}

type Config struct {
	Identity *identity.Identity
	Store    *trust.Store
	Name     string
	As       string
	Asker    Asker
	Timeout  time.Duration
}

func NameFrom(hostname string) string {
	short, _, _ := strings.Cut(hostname, ".")
	var name strings.Builder
	for _, character := range short {
		if name.Len() < trust.MaxNameLength && trust.ValidName(string(character)) {
			name.WriteRune(character)
		}
	}
	if name.Len() == 0 {
		return fallbackName
	}
	return name.String()
}

func OwnName() string {
	hostname, _ := os.Hostname()
	return NameFrom(hostname)
}

func Accept(listener net.Listener, config Config) (trust.Peer, error) {
	raw, err := listener.Accept()
	if err != nil {
		return trust.Peer{}, err
	}
	secured := tls.Server(raw, anyCertificate(config.Identity))
	return config.pairOver(secured, "", time.Now().Add(config.timeout()))
}

func Dial(address string, config Config) (trust.Peer, error) {
	host, port := splitAddress(address)
	deadline := time.Now().Add(config.timeout())
	dialer := net.Dialer{Deadline: deadline}
	raw, err := dialer.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return trust.Peer{}, fmt.Errorf("pairing: cannot connect to %s: %w", address, err)
	}
	secured := tls.Client(raw, anyCertificate(config.Identity))
	return config.pairOver(secured, net.JoinHostPort(host, strconv.Itoa(transport.DefaultPort)), deadline)
}

func splitAddress(address string) (host, port string) {
	if host, port, err := net.SplitHostPort(address); err == nil {
		return host, port
	}
	return strings.Trim(address, "[]"), strconv.Itoa(DefaultPort)
}

func anyCertificate(local *identity.Identity) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{local.Certificate},
		ClientAuth:         tls.RequireAnyClientCert,
		InsecureSkipVerify: true,
	}
}

func (c Config) timeout() time.Duration {
	if c.Timeout == 0 {
		return defaultTimeout
	}
	return c.Timeout
}

func (c Config) pairOver(secured *tls.Conn, addressToStore string, deadline time.Time) (trust.Peer, error) {
	defer secured.Close()
	if err := secured.SetDeadline(deadline); err != nil {
		return trust.Peer{}, err
	}
	candidate, err := c.meet(secured)
	if err != nil {
		return trust.Peer{}, interrupted(err)
	}
	candidate.Address = addressToStore
	if err := c.refusal(candidate); err != nil {
		writeAnswer(secured, false)
		return trust.Peer{}, err
	}
	question := Question{
		Name:      candidate.Name,
		OwnCode:   c.Identity.Fingerprint.Code(),
		OtherCode: candidate.Fingerprint.Code(),
	}
	if err := c.agree(secured, question, deadline); err != nil {
		return trust.Peer{}, err
	}
	if err := c.Store.Add(candidate); err != nil {
		return trust.Peer{}, err
	}
	return candidate, nil
}

func (c Config) meet(secured *tls.Conn) (trust.Peer, error) {
	if err := secured.Handshake(); err != nil {
		return trust.Peer{}, err
	}
	if err := writeMessage(secured, []byte(c.Name)); err != nil {
		return trust.Peer{}, err
	}
	proposed, err := readMessage(secured, trust.MaxNameLength)
	if err != nil {
		return trust.Peer{}, err
	}
	if !trust.ValidName(string(proposed)) {
		return trust.Peer{}, ErrMalformed
	}
	presented := secured.ConnectionState().PeerCertificates
	if len(presented) == 0 {
		return trust.Peer{}, ErrMalformed
	}
	name := c.As
	if name == "" {
		name = string(proposed)
	}
	return trust.Peer{Name: name, Fingerprint: identity.FingerprintOf(presented[0])}, nil
}

func (c Config) refusal(candidate trust.Peer) error {
	if candidate.Fingerprint == c.Identity.Fingerprint {
		return ErrSelf
	}
	if known, trusted := c.Store.Find(candidate.Fingerprint); trusted {
		return fmt.Errorf("%w as %q. To pair it again, first run: lanhop forget %s", ErrAlreadyPaired, known.Name, known.Name)
	}
	for _, peer := range c.Store.Peers() {
		if peer.Name == candidate.Name {
			return fmt.Errorf("%w: %q. Give the other machine another name here with --as NAME", ErrNameTaken, candidate.Name)
		}
	}
	return nil
}

type remoteAnswer struct {
	accepted bool
	err      error
}

func (c Config) agree(secured *tls.Conn, question Question, deadline time.Time) error {
	local := make(chan bool, 1)
	go func() { local <- c.Asker.Confirm(question) }()

	remote := make(chan remoteAnswer, 2)
	go func() {
		accepted, err := readAnswer(secured)
		remote <- remoteAnswer{accepted, err}
		if err == nil && accepted {
			remote <- remoteAnswer{err: readUntilGone(secured)}
		}
	}()

	expired := time.NewTimer(time.Until(deadline))
	defer expired.Stop()

	acceptedHere, acceptedThere := false, false
	for !acceptedHere || !acceptedThere {
		select {
		case accepted := <-local:
			if err := writeAnswer(secured, accepted); err != nil {
				return interrupted(err)
			}
			if !accepted {
				return ErrDeclined
			}
			acceptedHere = true
		case answer := <-remote:
			if answer.err != nil {
				return interrupted(answer.err)
			}
			if !answer.accepted {
				return ErrPeerDeclined
			}
			acceptedThere = true
		case <-expired.C:
			return ErrTimeout
		}
	}
	return nil
}

func interrupted(err error) error {
	if errors.Is(err, ErrMalformed) {
		return err
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return ErrTimeout
	}
	return fmt.Errorf("pairing: the connection ended before both machines answered: %w", err)
}
