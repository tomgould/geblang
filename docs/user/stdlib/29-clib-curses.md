# Terminal UI (`clib.curses`)

`import clib.curses as curses;` provides full-screen terminal UI via
libncurses. It covers window initialisation, character output, cursor
movement, keyboard input, and colour/attribute control.

The module uses the FFI layer so the `ffi` capability must be enabled.

> **Linux / macOS only.** libncurses ships as `libncursesw.so.6` on
> most Linux distributions (`libncurses-dev` / `ncurses-devel` package)
> and as `libncurses.dylib` on macOS via Homebrew. libtinfo may be a
> required transitive dependency on some distributions.

> **Single-owner constraint.** ncurses is global, single-screen,
> single-thread state. This module must be driven from ONE task only.
> Concurrent use from multiple async tasks is unsupported and cannot
> be made safe by serialising calls.

## Capability

Add to `geblang.yaml`:

```yaml
permissions:
  ffi:
    enabled: true
    libraries:
      - glob: libncursesw*
      - glob: libncurses*
      - glob: libtinfo*
```

For a standalone script:

```sh
geblang --allow-ffi 'libncursesw*' --allow-ffi 'libncurses*' script.gb
```

## Lifecycle

### `init(): void`

Initialises the screen. Calls `initscr`, then `cbreak` (disable line
buffering), `noecho` (suppress keystroke echo), and `keypad` (enable
arrow/function keys). Store the result internally as the stdscr
pointer. All screen functions require `init()` to have been called
first; calling them before `init()` throws a `RuntimeError`.

### `end(): void`

Ends the curses session (`endwin`) and resets the terminal to its
prior state. Safe to call before `init`.

## Screen functions

All functions below require `init()` to have been called first.

### `addStr(string s): void`

Writes `s` at the current cursor position.

### `mvAddStr(int y, int x, string s): void`

Writes `s` at position (y, x).

### `move(int y, int x): void`

Moves the cursor to row `y`, column `x`.

### `clear(): void`

Clears the screen.

### `refresh(): void`

Copies the virtual screen buffer to the terminal. Changes are not
visible until `refresh` is called.

### `getCh(): int`

Reads one keystroke (blocking) and returns its integer key code.

### `maxY(): int`

Returns the number of rows in the current terminal window.

### `maxX(): int`

Returns the number of columns in the current terminal window.

## Colours and attributes

### `startColor(): void`

Enables colour support. Call this before `initPair`.

### `initPair(int id, int fg, int bg): void`

Defines colour pair `id` (1..255) with foreground colour `fg` and
background colour `bg`. Use the exported colour constants for `fg`
and `bg`.

### `attrOn(int attrs): void`

Enables the given attribute bits for subsequent output.

### `attrOff(int attrs): void`

Disables the given attribute bits.

### `colorPair(int n): int`

Returns the attribute value for colour pair `n` (equivalent to
`COLOR_PAIR(n)` in C: `n * 256`). Pass the result to `attrOn`.

## Colour constants

| Constant | Value |
|---|---|
| `curses.BLACK` | `0` |
| `curses.RED` | `1` |
| `curses.GREEN` | `2` |
| `curses.YELLOW` | `3` |
| `curses.BLUE` | `4` |
| `curses.MAGENTA` | `5` |
| `curses.CYAN` | `6` |
| `curses.WHITE` | `7` |

## Attribute constants

| Constant | Value | Effect |
|---|---|---|
| `curses.A_NORMAL` | `0` | Default rendering |
| `curses.A_BOLD` | `2097152` | Bold / bright |
| `curses.A_UNDERLINE` | `131072` | Underline |
| `curses.A_REVERSE` | `262144` | Reverse video |
| `curses.A_DIM` | `1048576` | Dim |
| `curses.A_BLINK` | `524288` | Blinking |

## Example

This example requires a real terminal and the FFI flag above.

```gb
import clib.curses as curses;

curses.init();
curses.startColor();
curses.initPair(1, curses.GREEN, curses.BLACK);

let rows = curses.maxY();
let cols = curses.maxX();
let msg = "Hello from Geblang!";
let row = rows / 2;
let col = (cols - msg.length()) / 2;

curses.clear();
curses.attrOn(curses.colorPair(1) | curses.A_BOLD);
curses.mvAddStr(row, col, msg);
curses.attrOff(curses.colorPair(1) | curses.A_BOLD);
curses.mvAddStr(row + 1, col, "Press any key to exit.");
curses.refresh();
curses.getCh();
curses.end();
```

## Error behaviour

| Failure mode | Surface |
|---|---|
| libncurses not found or FFI not enabled | `RuntimeError` or `PermissionError` |
| Screen function called before `init()` | `RuntimeError: clib.curses: call init() first` |
