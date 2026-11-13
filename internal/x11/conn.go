//go:build linux

package x11

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

const (
	replyTimeout  = 5 * time.Second
	dialTimeout   = 2 * time.Second
	typeError     = 0
	typeReply     = 1
	typeGeneric   = 35
	sentEventFlag = 0x80
)

var (
	ErrClosed     = errors.New("x11: the connection to the X server is closed")
	ErrNotTrusted = errors.New("x11: the X server is run by another user")
)

func runByRootOrMe(socket net.Conn) error {
	unix, ok := socket.(*net.UnixConn)
	if !ok {
		return ErrNotTrusted
	}
	raw, err := unix.SyscallConn()
	if err != nil {
		return err
	}
	var credentials *syscall.Ucred
	var lookup error
	if err := raw.Control(func(fd uintptr) {
		credentials, lookup = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if lookup != nil {
		return lookup
	}
	if credentials.Uid != 0 && credentials.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("%w: user %d", ErrNotTrusted, credentials.Uid)
	}
	return nil
}

type RequestError struct {
	Code   byte
	Opcode byte
}

func (e RequestError) Error() string {
	return fmt.Sprintf("x11: request %d failed with error %d", e.Opcode, e.Code)
}

type response struct {
	reply []byte
	err   error
}

type conn struct {
	socket          net.Conn
	writing         sync.Mutex
	sequence        uint16
	waiting         sync.Mutex
	pending         map[uint16]chan response
	onEvent         func(event []byte)
	closing         sync.Once
	closed          chan struct{}
	failure         error
	root            uint32
	idBase          uint32
	idMask          uint32
	lastID          uint32
	maxRequestBytes int
}

func dial(d display, cookie []byte, onEvent func(event []byte)) (*conn, error) {
	socket, err := net.DialTimeout("unix", d.socket, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("x11: cannot reach display :%s: %w", d.number, err)
	}
	if err := runByRootOrMe(socket); err != nil {
		socket.Close()
		return nil, fmt.Errorf("x11: display :%s: %w", d.number, err)
	}
	c := &conn{
		socket:  socket,
		pending: make(map[uint16]chan response),
		onEvent: onEvent,
		closed:  make(chan struct{}),
	}
	if err := c.setup(cookie); err != nil {
		socket.Close()
		return nil, fmt.Errorf("x11: display :%s refused lanhop: %w", d.number, err)
	}
	go c.readLoop()
	return c, nil
}

func (c *conn) setup(cookie []byte) error {
	var name []byte
	if cookie != nil {
		name = []byte(cookieName)
	}
	r := newRequest('l', 0)
	r.b = r.b[:2]
	r.u16(11).u16(0).u16(uint16(len(name))).u16(uint16(len(cookie))).u16(0)
	r.raw(name).raw(cookie)
	if err := c.socket.SetDeadline(time.Now().Add(dialTimeout)); err != nil {
		return err
	}
	defer c.socket.SetDeadline(time.Time{})
	if _, err := c.socket.Write(r.b); err != nil {
		return err
	}
	head := make([]byte, 8)
	if _, err := io.ReadFull(c.socket, head); err != nil {
		return err
	}
	rest := make([]byte, int(binary.LittleEndian.Uint16(head[6:8]))*4)
	if _, err := io.ReadFull(c.socket, rest); err != nil {
		return err
	}
	if head[0] != 1 {
		return fmt.Errorf("setup status %d: %s", head[0], rest[:min(int(head[1]), len(rest))])
	}
	if len(rest) < 32 {
		return errors.New("setup reply too short")
	}
	c.idBase = binary.LittleEndian.Uint32(rest[4:8])
	c.idMask = binary.LittleEndian.Uint32(rest[8:12])
	vendorLength := int(binary.LittleEndian.Uint16(rest[16:18]))
	c.maxRequestBytes = int(binary.LittleEndian.Uint16(rest[18:20])) * 4
	formats := int(rest[21])
	screen := 32 + vendorLength + padding(vendorLength) + formats*8
	if len(rest) < screen+4 || rest[20] == 0 {
		return errors.New("setup reply has no screen")
	}
	c.root = binary.LittleEndian.Uint32(rest[screen : screen+4])
	return nil
}

func (c *conn) newID() uint32 {
	c.lastID++
	return c.idBase | (c.lastID & c.idMask)
}

func (c *conn) send(request []byte) error {
	_, err := c.write(request, false)
	return err
}

func (c *conn) call(request []byte) ([]byte, error) {
	answer, err := c.write(request, true)
	if err != nil {
		return nil, err
	}
	select {
	case got := <-answer:
		return got.reply, got.err
	case <-c.closed:
		return nil, c.err()
	case <-time.After(replyTimeout):
		return nil, fmt.Errorf("x11: the X server did not answer request %d", request[0])
	}
}

func (c *conn) write(request []byte, wantsReply bool) (chan response, error) {
	c.writing.Lock()
	defer c.writing.Unlock()
	select {
	case <-c.closed:
		return nil, c.err()
	default:
	}
	sequence := c.sequence + 1
	var answer chan response
	if wantsReply {
		answer = make(chan response, 1)
		c.waiting.Lock()
		c.pending[sequence] = answer
		c.waiting.Unlock()
	}
	err := c.socket.SetWriteDeadline(time.Now().Add(replyTimeout))
	if err == nil {
		_, err = c.socket.Write(request)
	}
	if err != nil {
		c.waiting.Lock()
		delete(c.pending, sequence)
		c.waiting.Unlock()
		c.fail(err)
		return nil, c.err()
	}
	c.sequence = sequence
	return answer, nil
}

func (c *conn) readLoop() {
	for {
		header := make([]byte, 32)
		if _, err := io.ReadFull(c.socket, header); err != nil {
			c.fail(err)
			return
		}
		switch header[0] {
		case typeError:
			c.deliver(binary.LittleEndian.Uint16(header[2:4]), response{err: RequestError{Code: header[1], Opcode: header[10]}})
		case typeReply:
			extra := int(binary.LittleEndian.Uint32(header[4:8])) * 4
			reply := append(header, make([]byte, extra)...)
			if _, err := io.ReadFull(c.socket, reply[32:]); err != nil {
				c.fail(err)
				return
			}
			c.deliver(binary.LittleEndian.Uint16(header[2:4]), response{reply: reply})
		default:
			if header[0]&^sentEventFlag == typeGeneric {
				extra := int(binary.LittleEndian.Uint32(header[4:8])) * 4
				if _, err := io.CopyN(io.Discard, c.socket, int64(extra)); err != nil {
					c.fail(err)
					return
				}
				continue
			}
			c.onEvent(header)
		}
	}
}

func (c *conn) deliver(sequence uint16, got response) {
	c.waiting.Lock()
	answer := c.pending[sequence]
	delete(c.pending, sequence)
	c.waiting.Unlock()
	if answer != nil {
		answer <- got
	}
}

func (c *conn) fail(err error) {
	c.closing.Do(func() {
		c.failure = err
		close(c.closed)
		c.socket.Close()
	})
}

func (c *conn) close() error {
	c.fail(ErrClosed)
	return nil
}

func (c *conn) err() error {
	if errors.Is(c.failure, ErrClosed) {
		return ErrClosed
	}
	return fmt.Errorf("%w: %v", ErrClosed, c.failure)
}

type request struct {
	b []byte
}

func newRequest(opcode, detail byte) *request {
	return &request{b: []byte{opcode, detail, 0, 0}}
}

func (r *request) u8(v byte) *request {
	r.b = append(r.b, v)
	return r
}

func (r *request) u16(v uint16) *request {
	r.b = binary.LittleEndian.AppendUint16(r.b, v)
	return r
}

func (r *request) u32(v uint32) *request {
	r.b = binary.LittleEndian.AppendUint32(r.b, v)
	return r
}

func (r *request) raw(data []byte) *request {
	r.b = append(r.b, data...)
	r.b = append(r.b, make([]byte, padding(len(data)))...)
	return r
}

func (r *request) done() []byte {
	r.b = append(r.b, make([]byte, padding(len(r.b)))...)
	binary.LittleEndian.PutUint16(r.b[2:4], uint16(len(r.b)/4))
	return r.b
}

func padding(n int) int {
	return (4 - n%4) % 4
}
