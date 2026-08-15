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
corruption rather than as a large chunk. Both readers agree on that number. It
used to be a real constraint on the writer, because a resize had to fit a whole
painted canvas into one chunk; nothing the writer emits comes close any more.

### Chunk order

```
Header
Resize     (the opening window — size and position)
Delta
Delta
Resize     (an expansion, or a mode cutting the canvas down)
Delta
...
Footer     (only once the recording is finished)
```

**A recording is a pure log of what happened.** Nothing in it restates the
canvas: a `Resize` says where the canvas now is, a `Delta` says what changed,
and a reader that has applied both holds the same canvas the writer had.

That is new as of `v7`, and it is the single biggest thing in the format. A
resize used to be a `Keyframe` carrying every painted pixel, because a pixel was
addressed by an offset *into* the canvas and every offset moved when the canvas
did — so the reader had to be handed the whole thing again. Measured on a real
200×200 season with 38 expansions:

| chunk    |   count | bytes    | share     |
| -------- | ------: | -------- | --------- |
| keyframe |      39 | 5.05 MB  | **91.4%** |
| delta    |    1735 | 0.47 MB  | 8.6%      |

91% of the file was the same artwork written out over and over. Now that a
pixel names a place on a plane rather than a slot in a buffer, a resize moves
the window and the contents come along, and the chunk that says so is twenty
bytes.

**There are no periodic keyframes either.** They were tried and removed before
that, for the same reason: a renderer consumes deltas anyway. The cost is that
seeking to time _T_ means replaying from the start — that is the trade, and it
is why a derived, rebuildable snapshot index is the obvious next thing to
build. It belongs beside the file, never in it.

### `Header`

One, first, always. Carries what is fixed for the whole recording: format
`version`, the season's `name`, `started_at`, `game_id`, `cooldown`, `ends_at`.

Notably **not** width and height. Those can change mid-recording, so they live
on `Resize` chunks instead, and `format.ReadHeader` refuses a file whose second
chunk is not one.

`record.v1` (`.pbr`) put the size on the header instead. Those fields are
**reserved** now, so such a file has nowhere left to declare a size — though in
practice the version check refuses it first, and says something more useful.

### `Resize`

The canvas' window: `timestamp`, `width`, `height`, `min_x`, `min_y`.

Written as chunk 2 of every recording, and again on every change of size or
position. **It carries no pixels.**

- **Both directions.** A canvas grows and, under a mode that plays with the
  size, shrinks. A cut destroys the pixels outside the new window, and every
  reader has to drop them itself — under the old scheme they were simply
  omitted from the keyframe, which did the same job by accident.
- **A window, not a delta of one.** It states where the canvas is, not how far
  it moved, so a reader that joins mid-file or seeks backwards needs no
  history to interpret it.
- **The only resize signal.** `molten` starts a new output segment on each,
  because an encoder cannot change frame size mid-stream.

### `Keyframe`

A restatement of contents: `timestamp` and `pixels`, and no geometry at all.
Applying one is applying a `Delta` whose changes happen to describe every
painted pixel rather than the ones that just moved.

**Nothing writes one.** It is kept because the format is otherwise a pure delta
log from byte 0, and a periodic snapshot is the obvious thing to add if that
ever needs a recovery point — or if a season is ever started on a canvas that
already has artwork on it. Implement it anyway; it costs the same five lines as
a `Delta`.

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

`total_pixels_placed` counts pixel changes across deltas only — a `Keyframe`
restates pixels their delta already counted.

---

## Coordinates

### A pixel names a place, not a slot

`PixelData.x` and `PixelData.y` are **signed coordinates on a fixed plane**.
The origin is the canvas' top-left corner as the season started, and it never
moves for the life of the recording.

That is the whole of it. The same pair means the same pixel in every chunk of
the file, before and after any number of resizes — no width to carry forward,
nothing to convert, nothing a reader can get subtly wrong.

It was not always so. Up to `v5` a pixel carried `id = y * width + x` against
whichever width was in force when its chunk was written, plus an `origin` on
each keyframe to convert back to something stable. Decoding such a file against
one global width silently misplaced every pixel written before the last resize,
and that was the single easiest thing to get wrong in a new reader.

Everything else on this page follows from the change. A resize can carry no
pixels because none of them moved; a rollback is a delta replay with one
author's placements left out, and no keyframe to filter back out afterwards;
and the 64 MiB chunk ceiling stopped constraining how large a canvas may be.

### The canvas is a window, and the window moves

A resize grows the canvas *around* the artwork according to an anchor, so the
artwork does not move — the canvas' own corner does. `min_x`/`min_y` on a
`Resize` say where that corner is, in the same coordinates the pixels use:

```
column = x - min_x
row    = y - min_y
```

A size alone never located a canvas. That was invisible while a pixel carried
an offset *into* the canvas, since an offset has nowhere else to point; now
that a pixel names a place on the plane, the chunk that changes the canvas has
to say which part of the plane it covers.

Two things worth stating plainly:

- **`min_x`/`min_y` go negative**, as soon as a season is expanded leftwards or
  upwards. They are a coordinate, not a distance.
- **Zero is the opening value**, which is right for every season: the origin is
  *defined* as the corner the canvas started on.

### Versions are checked, and this is why

`Header.version` was decorative until `v6` — every earlier break was caught
structurally, by a file having no sized opening keyframe. `v5` and `v6` are
structurally identical: same chunks, same field numbers, same everything. They
differ only in what the two numbers inside a `PixelData` mean.

Nothing in the bytes gives that away, so `format.ReadHeader` refuses a version
it does not know (`ErrUnknownVersion`) rather than reading a season and laying
every pixel out wrong. A wrong render is the kind of failure nobody notices
until they watch the video.

The gate paid for itself immediately: `v7` put a `Resize` on field 2 of a
`RecordingChunk`, a number that had been `reserved` since the first prototype.
Reusing a reserved number is normally how you produce a file that decodes into
the wrong message — it is safe here only because no pre-`v7` chunk is ever
handed to that dispatch.

### `PixelData.offset`

Milliseconds _before_ the enclosing chunk's timestamp that this pixel was
actually placed.

A delta covers a whole flush window, so without this a time-based renderer
applies the entire window at one instant and every pixel in it shares a
timestamp. Relative rather than absolute on purpose: bounded by the flush
interval, it costs 2–3 bytes as a varint where an absolute epoch-ms `uint64`
costs 7 — and the top 40 bits of that would be identical for every pixel in the
file.

`Keyframe` pixels carry it too, for the same reason a delta's do: a
restatement that cannot say when a pixel was painted flattens that part of the
season's history onto the moment it was written. Resizes used to be keyframes,
so an expansion did exactly that to the whole canvas; that is gone with them.

**Writers MUST set it whenever the placement time is known and differs from the
chunk's timestamp, and MUST leave it unset otherwise.** So unset means exactly
one thing — "no better information than the chunk's own timestamp" — and covers
every case where there is none: a time that was never recorded (a pre-v3 file,
or a pixel a moderator rollback restored, whose real paint time is long gone), a
time that is only a _bound_, and a clock that moved backwards between the
placement and the flush.

It stays `optional` deliberately, and must. Proto3 implicit presence would
default it to `0`, and `0` is precisely "placed at the chunk's timestamp" — so
the cases above would become indistinguishable from an exact placement, which is
a claim the writer cannot make. Nothing is saved by it either: an
implicit-presence scalar equal to `0` is not serialized at all.

Readers must not read unset as `0`. Fall back to the chunk's timestamp and carry
the fact that it is a bound.

### `Footer.canvas_checksum`

xxHash64, seed 0, over every non-blank pixel sorted by `(y, x)`, each digested
as a 12-byte little-endian record:

```
int32  x
int32  y
uint32 colour        (0x00RRGGBB)
```

The coordinates are the pixels' own, which is what makes the same artwork hash
the same either side of a resize — that used to need saying, back when a pixel
was addressed by an offset into the canvas. "Non-blank" means white with no
author and no tag is skipped, so a reader holding only what the file described
can reproduce the digest without inventing the pixels nobody painted.

It is **not** cryptographic and is not trying to be. It catches a second
implementation that decoded the file differently; it does nothing against
someone who can rewrite the footer along with the chunks.

---

## Reading a recording

The minimum a correct reader does:

1. Read chunk 1. It must be a `Header`, and its `version` must be one you
   know. Refuse it otherwise — see _Versions are checked_ above.
2. Read chunk 2. It must be a `Resize`; that is the only place a canvas window
   is ever declared.
3. For each chunk after that:
    - **resize** → move what you hold onto the new window, dropping anything
      that falls outside it, and cut a new output segment;
    - **keyframe** → apply its pixels, changing no geometry;
    - **delta** → apply its changes, using each pixel's `offset` for its real
      time;
    - **footer** → the recording is complete; stop.
4. Stop at the first chunk that fails to parse, and treat everything from there
   on as absent.

Step 3's first case is the one worth care. **Move, do not rebuild:** every
pixel keeps its coordinate across a resize, so the window changes and the
contents come along. Clip per axis while you do it — a column past the new
right edge is still a valid index one row down, so a single bounds test smears
a cut along the left edge instead of dropping it.

Unknown chunk types are ignored, not errors — the `oneof` is expected to grow.

---

## Using it as a library

Steps 1–4 above are the part a new reader gets wrong, so they are packaged
rather than left as prose. Three importable packages:

```bash
go get github.com/pixelate-it/molten
```

| Package                                 | What it gives you                                                             |
| --------------------------------------- | ----------------------------------------------------------------------------- |
| `github.com/pixelate-it/molten/ore`     | The protobuf types. The wire contract itself.                                 |
| `github.com/pixelate-it/molten/format`  | The container: framing, the header rules, and the coordinate/time arithmetic. |
| `github.com/pixelate-it/molten/canvas`  | Replaying chunks — into an image (`State`) or into a checksum (`Digest`).     |

Everything else — the ffmpeg pipe, the segment manager, the version string —
stays under `internal/`. It is the CLI's business, not the format's.

### Reading a finished recording

```go
f, err := os.Open("season.mltn")
if err != nil { return err }
defer f.Close()

r := format.NewChunkReader(bufio.NewReaderSize(f, 64*1024))

// enforces steps 1 and 2, including the version check, and hands back the
// opening Resize - whole, because its timestamp starts the season's clock
header, opening, err := format.ReadHeader(r)
if err != nil { return err }

state := canvas.NewState()
state.Reframe(format.ReadWindow(opening))

for {
    chunk, err := r.Next()
    if errors.Is(err, io.EOF) { break }
    if err != nil { return err }

    switch {
    case chunk.GetResize() != nil:
        // moves the canvas onto the new window, keeping what is still inside
        state.ApplyResize(chunk.GetResize())
        // an encoder cannot change frame size mid-stream: cut a segment here
    case chunk.GetKeyframe() != nil:
        state.ApplyKeyframe(chunk.GetKeyframe())
    case chunk.GetDelta() != nil:
        state.ApplyDelta(chunk.GetDelta())
    case chunk.GetFooter() != nil:
        // the season is over
    }
}
```

`state.ColorAt(x, y)` reads a pixel; `state.Image()` hands back the live
`*image.RGBA` (not a copy — applying anything else writes through it).

### Reading a recording that is still being written

Use `format.NewTailReader`, **not** `ChunkReader`, and give it the file rather
than a `bufio.Reader` — it needs to seek the thing it reads from.

```go
tr := format.NewTailReader(f)

for {
    chunk, err := tr.Next()
    if errors.Is(err, io.EOF) {
        time.Sleep(250 * time.Millisecond)   // nothing more *yet*
        continue
    }
    if err != nil { return err }
    ...
}
```

The difference is the whole point. A live consumer polls, so it meets the
writer mid-chunk — a backend flushing every few seconds keeps a poller at the
end of the file essentially always. `ChunkReader` consumes the length prefix
and whatever body had been flushed before reporting `io.EOF`, which leaves it
positioned *inside* a chunk body, so its next four bytes are body taken for a
length: either a corrupt-length error out of nowhere or a plausible number that
decodes garbage. `TailReader` rewinds to where the incomplete chunk began, so
sleeping and calling again resumes cleanly.

`io.EOF` from a `TailReader` never means "finished" — only "nothing more right
now". A `Footer` chunk is what says the season is over. `tr.Offset()` is worth
keeping across restarts; it is the only way to resume without replaying from
the beginning.

### Verifying a recording

`canvas.Digest` reproduces `Footer.canvas_checksum` — the format's only
cross-implementation check. Feed it the same chunks you feed a `State`, then
compare `Digest.Sum()` against the footer. That is exactly what `molten info`
prints, and what a new reader in another language should be tested against.

### Two traps the API takes care of

- **The canvas' corner moves, and a pixel's coordinates do not.** Applying
  chunks in order through a `State` or a `Digest` carries the corner forward
  for you; assuming the canvas starts at `(0, 0)` draws a leftward-expanded
  season entirely in the wrong place.
- **A delta's recorded order is first-touch order, not placement order.**
  `format.PlacementOrder(delta)` returns the pixels in the order they were
  painted, and `format.PlacedAt(chunkTimestamp, pixel)` resolves when one was.

---

## The CLI

```
molten <command> [flags]

  info     Inspect a .mltn recording
  render   Render a .mltn recording into a video
  version  Print version information
```

### `molten info -in season.mltn`

Header fields, canvas size and corner, every resize with its timestamp, and
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
| `-in`     | —         | Input recording, `.mltn`. Required.                                      |
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
so each `Resize` starts a new file: `out.mp4`, `out-2.mp4`, and so on.
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
