# Molten

A stream format for recording a collaborative pixel canvas, and a renderer that
turns one into video.

Molten exists because a canvas like [pixelbattle.fun](https://pixelbattle.fun/)
produces something a database is the wrong shape for: millions of small,
ordered, append-only events, of which you eventually want not the final state
but the _whole history_, played back. A `.mltn` file is that history — one file
per season, written as the season happens, replayable to any moment in it.

The format is protobuf over a trivial length-prefixed framing. The schema lives
in [`ore/`](ore/) and is the contract: the Go code here and the TypeScript
writer in
[pixelbattle-backend](https://github.com/pixelate-it/pixelbattle-backend) are
two implementations of it, not one implementation and one client.

---

## The file

### Framing

A recording is a bare sequence of chunks. No container header, no index, no
table of contents:

```
┌────────────┬──────────────────────┬────────────┬───────────────────┬─────
│ uint32 BE  │ RecordingChunk       │ uint32 BE  │ RecordingChunk    │ ...
│ length     │ (protobuf, `length`  │ length     │                   │
│            │  bytes)              │            │                   │
└────────────┴──────────────────────┴────────────┴───────────────────┴─────
```

Big-endian, four bytes, then that many bytes of an encoded
`molten.ore.RecordingChunk`. That is the entire container.

Two properties follow from it, and most of the design below is downstream of
them:

- **It is self-delimiting forwards only.** You can always find the next chunk;
  you can never find the previous one. Every reader either starts at byte 0 or
  at an offset it learned on an earlier pass.
- **It is append-only and crash-tolerant by construction.** A writer killed
  mid-chunk leaves a truncated tail, and a truncated tail costs exactly that
  chunk. Readers stop at the first chunk that does not parse and treat it as
  the end of the file — so a torn write never invalidates the history in front
  of it.

A length of `0`, or one past **64 MiB** (`maxChunkSize`), is treated as
corruption rather than as a large chunk. Both readers agree on that number, and
it is a real constraint on the writer — see _keyframes_ below.

### Chunk order

```
Header
Keyframe   (sized — establishes the canvas dimensions)
Delta
Delta
Keyframe   (sized — a resize, carries the whole canvas)
Delta
...
Footer     (only once the recording is finished)
```

**There are deliberately no periodic keyframes.** They were tried and removed:
a renderer consumes deltas anyway, and repeated full-canvas snapshots dominated
the file size for something nothing read. Every keyframe still in the format is
a _sized_ one — the opening one and one per canvas expansion. Consequently
`molten` treats **any** sized keyframe as a resize: it rebuilds its image from
scratch and cuts a new video segment.

The cost of having no periodic keyframes is that seeking to time _T_ means
replaying from the start. That is the trade, and it is why a derived,
rebuildable snapshot index is the obvious next thing to build — it belongs
beside the file, never in it.

### `Header`

One, first, always. Carries what is fixed for the whole recording: format
`version`, the season's `name`, `started_at`, `game_id`, `cooldown`, `ends_at`.

Notably **not** width and height. Those can change mid-recording, so they live
on the first `Keyframe` instead, and `format.ReadHeader` refuses a file whose
second chunk is not a sized keyframe.

Legacy `.pbr` (`record.v1`) files did put the size on the header.
`format.InitialSize` still prefers it when present, purely so those files keep
rendering.

### `Keyframe`

A full-canvas snapshot: `timestamp`, `pixels`, and — when it is a sizing or
resize keyframe — `width`, `height`, `origin_x`, `origin_y`.

- **Sparse.** Pixels that are white with nobody's name on them are omitted,
  because a resize fills the new canvas with white anyway.
- **Whole.** A resize keyframe must carry everything that survives the resize,
  because the reader blanks its image first.
- **One chunk.** Which is where the 64 MiB ceiling bites: a fully-painted
  canvas has to fit. At roughly 34 bytes per `PixelData` that runs out near
  ~1.97M pixels, which is why the backend caps a canvas at 1.5M.

### `Delta`

`timestamp` plus the pixels that changed. Written once per flush window (15s in
the backend), so its timestamp is the _flush_, not any individual placement —
see `PixelData.offset`.

A delta carries the **state** of each changed pixel at flush time, not a log of
the placements that produced it. Five repaints of one pixel inside one window
are one entry with the final colour. Anything counting placements out of a
recording is therefore counting _flush windows in which a pixel changed_.

### `Footer`

Written once, last, when the season is genuinely over — and only then. It
carries `timestamp`, `total_pixels_placed`, and `canvas_checksum`.

This exists because the framing alone cannot tell a finished recording from an
interrupted one: a clean shutdown, a redeploy and a crash all leave a file that
simply stops after a valid chunk. The footer is the difference. A writer that
finds one on a file it was about to append to knows to leave the file alone.

`total_pixels_placed` counts pixel changes across deltas only — a keyframe
restates pixels their delta already counted.

---

## Coordinates

### `PixelData.id` is local, and local changes

A pixel's position is `y * width + x`, where `width` is **whichever one was in
force when that chunk was written** — i.e. from the nearest preceding sized
keyframe, not the recording's final width.

This is the single easiest thing to get wrong when writing a new reader.
Decoding a whole file against one global width silently misplaces every pixel
written before the last resize. Carry the width forward per keyframe.

### Canonical coordinates and `origin`

A resize is grow-only, and it grows the canvas _around_ the existing artwork
according to an anchor. So unless the anchor was top-left, every pixel gets a
new `(x, y)` while nothing has actually moved: a 100×100 canvas grown to
200×200 anchored centre puts what was at `(10, 10)` at `(60, 60)`.

`origin_x`/`origin_y` on each keyframe are the accumulated anchor offset:

```
canonical = local - origin
local     = canonical + origin
```

Canonical coordinates are fixed to the _content_, so they are the ones that
survive an expansion — which is what a permalink, a client-side stencil, or a
cross-implementation checksum has to be expressed in. The anchor itself is
never recorded, only its result, so a reader cannot re-derive this: it has to
read it off the keyframe.

Two things worth stating plainly:

- The **origin** is never negative. Grow-only plus an anchor that only adds
  space around the old content means it can only move away from local `(0, 0)`.
- A **canonical coordinate** absolutely can be. Anything painted into space an
  expansion added on the left or the top has `local < origin`.

A recording written before this field has no origin; zero is the only
defensible reading, and it is correct for every recording that never resized.

### `PixelData.offset`

Milliseconds _before_ the enclosing chunk's timestamp that this pixel was
actually placed.

A delta covers a whole flush window, so without this a time-based renderer
applies the entire window at one instant and every pixel in it shares a
timestamp. Relative rather than absolute on purpose: bounded by the flush
interval, it costs 2–3 bytes as a varint where an absolute epoch-ms `uint64`
costs 7 — and the top 40 bits of that would be identical for every pixel in the
file.

Keyframes carry it too. A keyframe restates the whole canvas, so without
per-pixel offsets an expansion would flatten a season's placement times onto
the moment it happened.

Unset means "no better information than the chunk's own timestamp" — which is
what every recording written before the field looks like, and what a pixel
whose real time is only an upper bound has to fall back to.

### `Footer.canvas_checksum`

xxHash64, seed 0, over every non-blank pixel sorted by canonical `(y, x)`, each
digested as a 12-byte little-endian record:

```
int32  canonical_x
int32  canonical_y
uint32 colour        (0x00RRGGBB)
```

Canonical rather than raw ids, or the same physical artwork would hash
differently either side of a resize purely because the width changed. "Non-blank"
means what a keyframe means by it — white with no author and no tag is skipped —
so a reader that rebuilt the canvas from a keyframe can reproduce the digest.

It is **not** cryptographic and is not trying to be. It catches a second
implementation that decoded the file differently; it does nothing against
someone who can rewrite the footer along with the chunks.

---

## Reading a recording

The minimum a correct reader does:

1. Read chunk 1. It must be a `Header`.
2. Read chunk 2. It must be a `Keyframe`. Take the canvas size from it — or,
   for a legacy `.pbr`, from the header.
3. For each chunk after that:
    - **sized keyframe** → blank the canvas at the new size, adopt its
      `origin_x`/`origin_y`, apply its pixels, cut a new output segment;
    - **unsized keyframe** → apply its pixels;
    - **delta** → apply its changes, using each pixel's `offset` for its real
      time;
    - **footer** → the recording is complete; stop.
4. Stop at the first chunk that fails to parse, and treat everything from there
   on as absent.

Unknown chunk types are ignored, not errors — the `oneof` is expected to grow.

---

## The CLI

```
molten <command> [flags]

  info     Inspect a .mltn recording
  render   Render a .mltn recording into a video
  version  Print version information
```

### `molten info -in season.mltn`

Header fields, canvas size and origin, every resize with its timestamp, and
keyframe/delta/pixel counts. The cheapest way to find out whether a file is what
you think it is.

It also **verifies the file against its own footer**, which is the only
cross-implementation check the format has — this reader replays the whole
recording and recomputes both of the writer's numbers:

```
Sealed:        yes @ ts=1786066067572
  pixels:      36 recorded, matches
  checksum:    502fab6489e95440 recorded, matches
```

A `MISMATCH` on either line means this reader and the writer disagree about
what the file means, and prints both values so you can see which way. A
recording with no footer reports `Sealed: no` — it is still being written, or
its writer was interrupted. Anything after the footer is counted and reported
as a warning, and deliberately not applied: digesting a canvas the checksum was
never taken of would report a mismatch that says nothing about either side.

### `molten render -in season.mltn [-out out.mp4]`

| Flag      | Default   | Meaning                                                                  |
| --------- | --------- | ------------------------------------------------------------------------ |
| `-in`     | —         | Input recording, `.mltn` or legacy `.pbr`. Required.                     |
| `-out`    | `out.mp4` | Output path. Numbered per segment if the canvas was resized.             |
| `-fps`    | `30`      | Output frame rate.                                                       |
| `-mode`   | `time`    | `time` — real elapsed time, sped up. `activity` — a frame per N changes. |
| `-speed`  | `3600`    | Recorded-time speedup factor (`-mode time`).                             |
| `-step`   | `50`      | Pixel changes per emitted frame (`-mode activity`).                      |
| `-scale`  | `1`       | Whole-number nearest-neighbour magnification.                            |
| `-ffmpeg` | —         | Extra ffmpeg output argument, repeatable: `-ffmpeg -crf -ffmpeg 18`.     |

The two modes answer different questions. `time` shows what a season _felt_
like — quiet nights stay quiet. `activity` shows what was _done_ — every change
gets equal screen time regardless of when it happened.

**Resizes cut segments.** Video encoders cannot change frame size mid-stream,
so each sized keyframe starts a new file: `out.mp4`, `out-2.mp4`, and so on.
Stitch them afterwards if you want one video.

`-scale` is checked up front against libx264's 8192-pixel limit, because ffmpeg
would otherwise refuse _after_ a render that can take minutes.

Rendering shells out to `ffmpeg`, which must be on `PATH`.

---

## Building

```bash
make build      # -> bin/molten
make test
make proto      # regenerate ore/*.pb.go, needs protoc + protoc-gen-go
```

Or via Nix, which pins the toolchain and wires `ffmpeg` onto the built
binary's `PATH`:

```bash
nix build
nix develop
```

### Changing the schema

`ore/*.proto` is a shared contract with the backend. Changing it means:

1. edit the `.proto` here;
2. `make proto` for the Go side;
3. `bun run proto:generate` in pixelbattle-backend for the TypeScript side;
4. bump `FORMAT_VERSION` in the backend's `record.ts` if the change is worth a
   reader knowing about.

**Add fields, never renumber or reuse them.** Recordings on disk outlive every
version of this code, and a field number that changes meaning silently
misreads them. `RecordingChunk` already reserves field 2 from a removed
message; that is the pattern.

Nothing currently branches on `Header.version` — every change so far has been a
new optional field, which old readers skip and new readers default. It is a
diagnostic, not a parsing switch, and it should stay that way for as long as it
can.
