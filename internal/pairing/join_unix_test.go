//go:build unix

package pairing

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

func TestAJoinThatFinishesWhileTheWaitIsStoppedIsStillReported(t *testing.T) {
	central, newcomer := newMachine(t, "imac"), newMachine(t, "office")
	os.Remove(central.storePath)
	if err := syscall.Mkfifo(central.storePath, 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}
	wait := waitingForJoin(t, central, "office-mac")

	joined := make(chan outcome, 1)
	go func() { joined <- joinsWith(t, newcomer, wait.address, wait.code) }()
	storing, err := os.OpenFile(central.storePath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	wait.stop()
	time.Sleep(shortTimeout)
	storing.WriteString(`{"peers":[]}`)
	storing.Close()

	if got := result(t, wait.finished); got.err != nil || got.peer.Name != "office-mac" {
		t.Fatalf("the wait ended with %+v, %v, want the machine that was being stored", got.peer, got.err)
	}
	if got := result(t, joined); got.err != nil {
		t.Fatalf("the new machine got %v, want the welcome", got.err)
	}
	expectTrusted(t, central, trust.Peer{Name: "office-mac", Fingerprint: newcomer.identity.Fingerprint})
}
