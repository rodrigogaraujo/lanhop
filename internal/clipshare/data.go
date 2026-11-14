package clipshare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/arrival"
	"github.com/rodrigogaraujo/lanhop/internal/filestream"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
)

const (
	relayBuffer     = 64 << 10
	progressAtLeast = 256 << 10
)

var (
	errTransferStopped   = errors.New("clipshare: the transfer was stopped")
	errConnectionClosed  = errors.New("clipshare: the control connection to that machine is closed")
	errControlLost       = errors.New("clipshare: the control connection to the machine with the keyboard was lost")
	errReceiverTooSlow   = errors.New("clipshare: the machine receiving the files did not say that every file arrived in time")
	errStoppedByCentral  = errors.New("clipshare: the machine with the keyboard stopped the transfer")
	errStoppedByThisSide = errors.New("clipshare: this machine stopped the transfer")
	errEndedByTheCentral = errors.New("the machine with the keyboard ended the connection for files")
	errTooSlow           = errors.New("clipshare: the files moved too slowly")
	errRelayStalled      = fmt.Errorf("%w: the sending machine moved fewer than %d KiB and the receiving machine did not say that every file arrived", errTooSlow, progressAtLeast>>10)
)

type dataConn interface {
	io.ReadWriteCloser
	SetDeadline(deadline time.Time) error
	SetWriteDeadline(deadline time.Time) error
	TellAllArrived() error
	AwaitAllArrived(ctx context.Context) error
}

type arrivals struct {
	*arrival.Arrival
}

func (a arrivals) File(name string, size int64) (filestream.IncomingFile, error) {
	file, err := a.Arrival.File(name, size)
	if err != nil {
		return nil, err
	}
	return file, nil
}

type watchedData struct {
	dataConn
	silence time.Duration

	mutex     sync.Mutex
	stopped   bool
	heard     bool
	waited    time.Duration
	inWindow  int
	lastMoved time.Time
}

func watch(conn dataConn, silence time.Duration) *watchedData {
	return &watchedData{dataConn: conn, silence: silence, lastMoved: time.Now()}
}

func (w *watchedData) Read(buffer []byte) (int, error) {
	started := w.beforeReading()
	read, err := w.dataConn.Read(buffer)
	return read, w.afterReading(started, read, err)
}

func (w *watchedData) beforeReading() time.Time {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	started := time.Now()
	if !w.stopped {
		w.dataConn.SetDeadline(started.Add(w.silence - w.waited))
	}
	return started
}

func (w *watchedData) afterReading(started time.Time, read int, err error) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	now := time.Now()
	if w.heard {
		w.waited += now.Sub(started)
	}
	if read > 0 {
		w.lastMoved = now
		if !w.heard {
			w.heard, w.waited = true, 0
		}
		w.inWindow += read
		if w.inWindow >= progressAtLeast {
			w.inWindow, w.waited = 0, 0
		}
	}
	if err == nil || w.stopped || !errors.Is(err, os.ErrDeadlineExceeded) {
		return err
	}
	if !w.heard {
		return fmt.Errorf("%w: nothing arrived in %s", errTooSlow, w.silence)
	}
	return fmt.Errorf("%w: fewer than %d KiB arrived in %s of waiting for them", errTooSlow, progressAtLeast>>10, w.silence)
}

func (w *watchedData) Write(data []byte) (int, error) {
	w.boundTheNextWrite()
	return w.dataConn.Write(data)
}

func (w *watchedData) TellAllArrived() error {
	w.boundTheNextWrite()
	return w.dataConn.TellAllArrived()
}

func (w *watchedData) boundTheNextWrite() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if !w.stopped {
		w.dataConn.SetWriteDeadline(time.Now().Add(w.silence))
	}
}

func (w *watchedData) SetDeadline(deadline time.Time) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.stopped = true
	return w.dataConn.SetDeadline(deadline)
}

func (w *watchedData) SetWriteDeadline(deadline time.Time) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.stopped = true
	return w.dataConn.SetWriteDeadline(deadline)
}

func (w *watchedData) AwaitAllArrived(ctx context.Context) error {
	limited, cancel := context.WithTimeout(ctx, w.silence)
	defer cancel()
	err := w.dataConn.AwaitAllArrived(limited)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return errReceiverTooSlow
	}
	return err
}

func (w *watchedData) lastMovedAt() time.Time {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.lastMoved
}

func relay(ctx context.Context, from, to *watchedData) error {
	copied := make(chan error, 1)
	go func() {
		_, err := io.CopyBuffer(to, from, make([]byte, relayBuffer))
		copied <- err
	}()
	arrived := make(chan error, 1)
	go func() { arrived <- to.dataConn.AwaitAllArrived(ctx) }()
	select {
	case err := <-arrived:
		return tellTheSender(from, err)
	case copyErr := <-copied:
		patience := time.NewTimer(time.Until(from.lastMovedAt().Add(from.silence)))
		defer patience.Stop()
		select {
		case err := <-arrived:
			return tellTheSender(from, err)
		case <-patience.C:
			return stalled(copyErr)
		}
	}
}

func tellTheSender(from *watchedData, arrivedErr error) error {
	if arrivedErr != nil {
		return arrivedErr
	}
	from.TellAllArrived()
	return nil
}

func stalled(copyErr error) error {
	switch {
	case copyErr == nil:
		return errReceiverTooSlow
	case errors.Is(copyErr, errTooSlow):
		return errRelayStalled
	}
	return copyErr
}

func endedByTheOtherEnd(err error) bool {
	for _, ended := range []error{net.ErrClosed, io.EOF, io.ErrUnexpectedEOF, syscall.ECONNRESET, syscall.EPIPE, transport.ErrDataEndedEarly, transport.ErrDataRefused} {
		if errors.Is(err, ended) {
			return true
		}
	}
	return false
}

func forTheLog(err error) string {
	var shown strings.Builder
	for _, r := range err.Error() {
		if strconv.IsPrint(r) {
			shown.WriteRune(r)
			continue
		}
		quoted := strconv.QuoteRune(r)
		shown.WriteString(quoted[1 : len(quoted)-1])
	}
	return shown.String()
}

func kibibytesRoundedUp(bytes int64) int32 {
	return int32((bytes + 1023) / 1024)
}

func noteLeftOut(log io.Writer, skipped int) {
	if skipped > 0 {
		fmt.Fprintf(log, "The file copy on this machine leaves out %d %s inside its folders, which lanhop never sends.\n", skipped, notePlural(skipped, "link or special file", "links or special files"))
	}
}

func notePlural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

func waitAtMost(group *sync.WaitGroup, limit time.Duration) {
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func closeInTheBackground(conns ...io.Closer) {
	if len(conns) == 0 {
		return
	}
	go func() {
		for _, conn := range conns {
			conn.Close()
		}
	}()
}
