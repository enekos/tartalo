package nativegen_test

import (
	"strings"
	"testing"
)

func TestNativeRecordSpread(t *testing.T) {
	bin := build(t, `
		type Person = { name: string, age: number }
		func main(): void {
			let alice: Person = Person{name: "Alice", age: 30}
			let older: Person = Person{...alice, age: 31}
			echo(older.name + "/" + str(older.age))
			echo(alice.name + "/" + str(alice.age))
		}
	`)
	got := runBin(t, bin)
	want := "Alice/31\nAlice/30\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeCastBetweenRecords(t *testing.T) {
	bin := build(t, `
		type RawUser   = { name: string, age: number, email: string }
		type ShortUser = { name: string, age: number }
		func main(): void {
			let raw: RawUser = RawUser{name: "Alice", age: 30, email: "a@x"}
			let short: ShortUser = raw as ShortUser
			echo(short.name + "/" + str(short.age))
		}
	`)
	got := strings.TrimRight(runBin(t, bin), "\n")
	if got != "Alice/30" {
		t.Errorf("got %q", got)
	}
}

func TestNativeSpreadNestedRecord(t *testing.T) {
	bin := build(t, `
		type Addr = { city: string, zip: number }
		type Person = { name: string, addr: Addr }
		func main(): void {
			let alice: Person = Person{
				name: "Alice",
				addr: Addr{city: "Madrid", zip: 28001},
			}
			let renamed: Person = Person{...alice, name: "Alicia"}
			echo(renamed.name + " in " + renamed.addr.city)
		}
	`)
	got := strings.TrimRight(runBin(t, bin), "\n")
	if got != "Alicia in Madrid" {
		t.Errorf("got %q", got)
	}
}
