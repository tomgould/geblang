# Image (`image`)

A portable native raster-image module. No system library is required;
it is backed entirely by Go's standard image packages. PNG, JPEG,
GIF, and WebP decoding are supported in every normal Geblang build.

```gb
import image;

let img = image.load("photo.jpg");
let thumb = img.resize(128, 128);
thumb.save("thumb.png", "png");
img.close();
thumb.close();
```

## Factories

### `image.load(string path): Image`

Loads and decodes a file from disk. The format is detected
automatically. Throws `RuntimeError` if the file cannot be read
or the format is not recognised.

### `image.loadBytes(bytes data): Image`

Decodes an image from raw bytes already in memory. Useful when
the image has been fetched over HTTP or read from an archive.
Throws `RuntimeError` on an unrecognised format.

### `image.blank(int w, int h): Image`

Creates a new fully-transparent (RGBA) image of the given
dimensions. Useful for generating images programmatically
without a source file.

## `Image` class

All transform methods return a **new** `Image`; the source
image is unchanged and remains valid. Call `close()` on each
handle you no longer need, or let the process exit reclaim
everything.

### `width(): int`

Returns the image width in pixels.

### `height(): int`

Returns the image height in pixels.

### `resize(int w, int h): Image`

Scales the image to `w` x `h` pixels using CatmullRom (bicubic)
resampling. Returns a new `Image`.

### `crop(int x, int y, int w, int h): Image`

Extracts a rectangle at offset `(x, y)` with dimensions `w` x
`h`. Returns a new `Image`. Throws `RuntimeError` if the
rectangle extends outside the source bounds.

### `rotate(int degrees): Image`

Rotates the image. `degrees` must be a multiple of 90; any other
value throws `RuntimeError`. Returns a new `Image`.

### `encode(string format): bytes`

Encodes the image to the given format and returns the result as
raw bytes. `format` must be one of `"png"`, `"jpeg"` (or `"jpg"`),
or `"gif"`. WebP is supported for decoding only - pass `"png"` to
re-encode a WebP source.

### `save(string path, string format): void`

Encodes the image and writes it to `path`. Equivalent to encoding
and then writing the bytes to disk. The same format strings apply
as for `encode`.

### `close(): void`

Releases the native handle. After `close()` the `Image` value
must not be used. Calling `close()` is optional but recommended
for long-running programs that process many images.

## Supported formats

| Format | Decode | Encode |
|--------|--------|--------|
| PNG    | yes    | yes    |
| JPEG   | yes    | yes    |
| GIF    | yes    | yes    |
| WebP   | yes    | no     |

WebP images can be loaded with `image.load` or `image.loadBytes`,
but `encode` and `save` do not support `"webp"` as a format
argument.

## Example: load, transform, and save

```gb
import image;
import io;

let src = image.load("input.png");
io.println("source: ${src.width()}x${src.height()}");

/* crop a central region, then scale it down */
let cx = src.width() / 4;
let cy = src.height() / 4;
let cropped = src.crop(cx, cy, src.width() / 2, src.height() / 2);
let out = cropped.resize(256, 256);

out.save("output.png", "png");
io.println("saved output.png");

src.close();
cropped.close();
out.close();
```

## Example: encode to bytes

```gb
import image;
import io;

let img = image.blank(64, 64);
let data = img.encode("png");
io.println("png bytes: ${data.length()}");
img.close();
```

## Handle lifetime

- Each factory (`load`, `loadBytes`, `blank`) and each transform
  (`resize`, `crop`, `rotate`) returns an independent `Image` handle.
- Closing a source does not affect any derived images.
- In short scripts and CLI tools, skipping `close()` is fine; the
  OS reclaims all handles on exit.
- In long-running programs that process many images, call `close()`
  on each handle once it is no longer needed.
