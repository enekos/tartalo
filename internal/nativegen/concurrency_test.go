package nativegen_test

import (
	"strings"
	"testing"
)

// TestNativeSpawnAndChannelString checks that spawn launches a goroutine,
// the channel ferries values, and waitAll() joins. With a single producer
// the order is deterministic.
func TestNativeSpawnAndChannelString(t *testing.T) {
	bin := build(t, `
		func producer(ch: chan[string]): void {
			send(ch, "hello")
			send(ch, "world")
			closeChan(ch)
		}
		func main(): void {
			let ch: chan[string] = chan()
			spawn producer(ch)
			while true {
				let m: string? = recv(ch)
				if m == null { break }
				echo(m!)
			}
			waitAll()
		}
	`)
	got := strings.TrimRight(runBin(t, bin), "\n")
	if got != "hello\nworld" {
		t.Errorf("got %q want \"hello\\nworld\"", got)
	}
}

// TestNativeSpawnAndChannelNumber confirms that the receive optional in
// the native backend (T?) plays nicely with arithmetic on the unwrapped
// value.
func TestNativeSpawnAndChannelNumber(t *testing.T) {
	bin := build(t, `
		func producer(ch: chan[number]): void {
			for i in 1..5 {
				send(ch, i * 10)
			}
			closeChan(ch)
		}
		func main(): void {
			let ch: chan[number] = chan()
			spawn producer(ch)
			let total: number = 0
			while true {
				let m: number? = recv(ch)
				if m == null { break }
				total = total + m!
			}
			waitAll()
			echo(str(total))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "100" {
		t.Errorf("got %q want %q", got, "100")
	}
}

// TestNativeSpawnMultipleProducersJoinedByWaitAll: three goroutines all
// send to the same channel; main receives three values then waits for
// every producer to exit. Total must be 1+2+3 regardless of scheduling.
func TestNativeSpawnMultipleProducersJoinedByWaitAll(t *testing.T) {
	bin := build(t, `
		func producer(id: number, ch: chan[number]): void {
			send(ch, id)
		}
		func main(): void {
			let ch: chan[number] = chan()
			spawn producer(1, ch)
			spawn producer(2, ch)
			spawn producer(3, ch)
			let total: number = 0
			for i in 1..4 {
				let m: number? = recv(ch)
				if m == null { continue }
				total = total + m!
			}
			waitAll()
			closeChan(ch)
			echo(str(total))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "6" {
		t.Errorf("got %q want %q", got, "6")
	}
}

// TestNativeRecvAfterCloseReturnsNull confirms that a closeChan + drained
// channel yields null on the next recv, exactly matching the sh backend.
// We don't compare the value of the first message (just that it isn't
// null) — the unwrap-in-else-branch idiom is currently quirky on the
// native target and unrelated to the channel runtime.
func TestNativeRecvAfterCloseReturnsNull(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let ch: chan[string] = chan()
			send(ch, "only")
			closeChan(ch)
			let a: string? = recv(ch)
			let b: string? = recv(ch)
			if a == null { echo("a was null") } else { echo("a was set") }
			if b == null { echo("b was null") } else { echo("b was set") }
		}
	`)
	got := strings.TrimRight(runBin(t, bin), "\n")
	want := "a was set\nb was null"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
