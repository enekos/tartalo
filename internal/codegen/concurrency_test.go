package codegen_test

import (
	"strings"
	"testing"
)

// TestSpawnAndChannelStringEnd2End: a single producer sends two values then
// closes; the consumer drains via recv-until-null and prints them in order.
// The ordering is deterministic because there's only one producer.
func TestSpawnAndChannelStringEnd2End(t *testing.T) {
	sh := compile(t, `
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
	out := runShell(t, sh)
	got := strings.TrimRight(out, "\n")
	if got != "hello\nworld" {
		t.Errorf("got %q want \"hello\\nworld\"\n--script--\n%s", got, sh)
	}
}

// TestSpawnAndChannelNumberEnd2End exercises a number-typed channel and
// confirms the recv result lands in arithmetic context (so arithmetic on
// the unwrapped value works).
func TestSpawnAndChannelNumberEnd2End(t *testing.T) {
	sh := compile(t, `
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
	out := strings.TrimRight(runShell(t, sh), "\n")
	if out != "100" {
		t.Errorf("got %q want %q\n--script--\n%s", out, "100", sh)
	}
}

// TestSpawnMultipleProducersAreAllJoined verifies that `waitAll()` blocks
// until every spawned agent has completed. Each producer sends a unique
// marker and increments a shared counter via the channel itself; the
// receiver totals the markers and we assert all messages arrived before
// the final echo.
func TestSpawnMultipleProducersAreAllJoined(t *testing.T) {
	sh := compile(t, `
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
	out := strings.TrimRight(runShell(t, sh), "\n")
	if out != "6" {
		t.Errorf("got %q want %q\n--script--\n%s", out, "6", sh)
	}
}

// --- error cases (checker) -------------------------------------------------

func TestChanRequiresTypedContext(t *testing.T) {
	errs := checkOnly(t, `
		func main(): void {
			let ch = chan()
		}
	`)
	if !containsErr(errs, "chan requires a typed context") {
		t.Errorf("expected typed-context error, got: %v", errs)
	}
}

func TestSendChecksValueType(t *testing.T) {
	errs := checkOnly(t, `
		func main(): void {
			let ch: chan[number] = chan()
			send(ch, "not a number")
		}
	`)
	if !containsErr(errs, "not assignable") {
		t.Errorf("expected element-type mismatch, got: %v", errs)
	}
}

func TestSpawnRejectsBuiltin(t *testing.T) {
	errs := checkOnly(t, `
		func main(): void {
			spawn echo("hi")
		}
	`)
	if !containsErr(errs, "user-declared function") {
		t.Errorf("expected builtin-rejection error, got: %v", errs)
	}
}

func TestSpawnRequiresVoidReturn(t *testing.T) {
	errs := checkOnly(t, `
		func produce(): number { return 42 }
		func main(): void {
			spawn produce()
		}
	`)
	if !containsErr(errs, "must return void") {
		t.Errorf("expected void-return error, got: %v", errs)
	}
}

func TestChanRejectsNonScalarElem(t *testing.T) {
	errs := checkOnly(t, `
		func main(): void {
			let ch: chan[string[]] = chan()
		}
	`)
	if !containsErr(errs, "scalar primitive") {
		t.Errorf("expected scalar-element error, got: %v", errs)
	}
}
