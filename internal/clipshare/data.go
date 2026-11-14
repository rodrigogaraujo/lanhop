package clipshare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/arrival"
	"github.com/rodrigogaraujo/lanhop/internal/filestream"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
)

const relayBuffer = 64 << 10

var (
	errTransferStopped   = errors.New("clipshare: the transfer was stopped")
	errConnectionClosed  = errors.New("clipshare: the control connection to that machine is closed")
	errControlLost       = errors.New("clipshare: the control connection to the machine with the keyboard was lost")
	errSenderEndedEarly  = errors.New("clipshare: the machine sending the files ended its data connection before every file arrived")
	errReceiverTooSlow   = errors.New("clipshare: the machine receiving the files did not say that every file arrived in time")
	errStoppedByCentral  = errors.New("clipshare: the machine with the keyboard stopped the transfer")
	errStoppedByThisSide = errors.New("clipshare: this machine stopped the transfer")
)

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
	*transport.DataConn
	silence time.Duration

	mutex   sync.Mutex
	stopped bool
}

func watch(conn *transport.DataConn, silence time.Duration) *watchedData {
	return &watchedData{DataConn: conn, silence: silence}
}

func (w *watchedData) Read(buffer []byte) (int, error) {
	w.mutex.Lock()
	if !w.stopped {
		w.DataConn.SetReadDeadline(time.Now().Add(w.silence))
	}
	w.mutex.Unlock()
	return w.DataConn.Read(buffer)
}

func (w *watchedData) Write(data []byte) (int, error) {
	w.mutex.Lock()
	if !w.stopped {
		w.DataConn.SetWriteDeadline(time.Now().Add(w.silence))
	}
	w.mutex.Unlock()
	return w.DataConn.Write(data)
}

func (w *watchedData) SetDeadline(deadline time.Time) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.stopped = true
	return w.DataConn.SetDeadline(deadline)
}

func (w *watchedData) SetWriteDeadline(deadline time.Time) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.stopped = true
	return w.DataConn.SetWriteDeadline(deadline)
}

func (w *watchedData) AwaitAllArrived(ctx context.Context) error {
	limited, cancel := context.WithTimeout(ctx, w.silence)
	defer cancel()
	err := w.DataConn.AwaitAllArrived(limited)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return errReceiverTooSlow
	}
	return err
}

func relay(ctx context.Context, from, to *watchedData) error {
	copied := make(chan error, 1)
	go func() {
		_, err := io.CopyBuffer(to, from, make([]byte, relayBuffer))
		copied <- err
	}()
	arrived := make(chan error, 1)
	go func() { arrived <- to.DataConn.AwaitAllArrived(ctx) }()
	select {
	case err := <-arrived:
		return tellTheSender(from, err)
	case copyErr := <-copied:
		patience := time.NewTimer(to.silence)
		defer patience.Stop()
		select {
		case err := <-arrived:
			return tellTheSender(from, err)
		case <-patience.C:
			if copyErr == nil {
				copyErr = errSenderEndedEarly
			}
			return copyErr
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
