package bytecode_test

import "testing"

func TestParityEnumMethodNullaryAndAssociated(t *testing.T) {
	runParity(t, `import io;
enum Status {
    Active, Suspended, Closed(string);

    func isTerminal(): bool {
        return match (this) {
            case Status.Closed(string r) => true;
            default => false;
        };
    }
    func describe(): string {
        return match (this) {
            case Status.Active => "active";
            case Status.Suspended => "suspended";
            case Status.Closed(string r) => "closed: " + r;
        };
    }
}
let a = Status.Active;
let c = Status.Closed("fraud");
io.println(a.isTerminal());
io.println(c.isTerminal());
io.println(a.describe());
io.println(c.describe());
`, "false\ntrue\nactive\nclosed: fraud\n")
}

func TestParityEnumMethodCallsSibling(t *testing.T) {
	runParity(t, `import io;
enum Status {
    Active, Closed(string);
    func describe(): string {
        return match (this) {
            case Status.Active => "active";
            case Status.Closed(string r) => "closed: " + r;
        };
    }
    func loud(): string { return this.describe() + "!"; }
}
io.println(Status.Closed("x").loud());
`, "closed: x!\n")
}

func TestParityEnumMethodArguments(t *testing.T) {
	runParity(t, `import io;
enum Counter {
    Zero, N(int);
    func plus(int k): int {
        return match (this) {
            case Counter.Zero => k;
            case Counter.N(int n) => n + k;
        };
    }
}
io.println(Counter.N(3).plus(4));
io.println(Counter.Zero.plus(9));
`, "7\n9\n")
}

func TestParityEnumInterfaceTypedDispatch(t *testing.T) {
	runParity(t, `import io;
interface Describable { func describe(): string; }
enum Status implements Describable {
    Active, Closed(string);
    func describe(): string {
        return match (this) {
            case Status.Active => "active";
            case Status.Closed(string r) => "closed: " + r;
        };
    }
}
func render(Describable d): string { return d.describe(); }
Describable x = Status.Active;
io.println(x.describe());
io.println(render(Status.Closed("z")));
`, "active\nclosed: z\n")
}

func TestParityEnumInterfaceDefault(t *testing.T) {
	runParity(t, `import io;
interface Greeter {
    func name(): string;
    func greet(): string { return "Hi, " + this.name(); }
}
enum Person implements Greeter {
    Alice, Bob;
    func name(): string {
        return match (this) {
            case Person.Alice => "Alice";
            default => "Bob";
        };
    }
}
io.println(Person.Alice.greet());
io.println(Person.Bob.greet());
`, "Hi, Alice\nHi, Bob\n")
}

func TestParityEnumMethodFromFreeAndClassAndMatch(t *testing.T) {
	runParity(t, `import io;
enum Dir {
    North, South;
    func opposite(): string {
        return match (this) {
            case Dir.North => "south";
            default => "north";
        };
    }
}
func free(Dir d): string { return d.opposite(); }
class Walker {
    func step(Dir d): string { return d.opposite(); }
}
let m = match (Dir.North) {
    case Dir.North => Dir.North.opposite();
    default => "x";
};
io.println(free(Dir.North));
io.println(Walker().step(Dir.South));
io.println(m);
`, "south\nnorth\nsouth\n")
}

func TestParityEnumMethodRecursive(t *testing.T) {
	runParity(t, `import io;
enum Nat {
    Zero, Succ(int);
    func toInt(): int {
        return match (this) {
            case Nat.Zero => 0;
            case Nat.Succ(int n) => n;
        };
    }
    func describe(): string {
        return match (this) {
            case Nat.Zero => "zero";
            case Nat.Succ(int n) => "succ of " + ("${n}");
        };
    }
}
io.println(Nat.Succ(3).toInt());
io.println(Nat.Succ(3).describe());
`, "3\nsucc of 3\n")
}

func TestParityEnumInstanceofInterface(t *testing.T) {
	runParity(t, `import io;
interface Describable { func describe(): string; }
enum Status implements Describable {
    Active;
    func describe(): string { return "active"; }
}
let s = Status.Active;
io.println(s instanceof Status);
io.println(s instanceof Describable);
io.println(s instanceof Status.Active);
`, "true\ntrue\ntrue\n")
}

func TestParityBareEnumStillWorks(t *testing.T) {
	runParity(t, `import io;
enum Color { Red, Green, Blue }
io.println(Color.Red);
io.println(Color.Green == Color.Green);
let d = match (Color.Blue) {
    case Color.Red => "r";
    case Color.Blue => "b";
    default => "?";
};
io.println(d);
`, "Color.Red\ntrue\nb\n")
}
