# VedutaOS

A small console that plays [Veduta](https://github.com/riftbane/veduta) games: a Raspberry
Pi behind a 320×240 panel, a gamepad, and a dashboard listing the games present on its SD
card. Games arrive by dragging a folder onto the card from a PC — there is no store, no
installer and no network.

**Nothing boots yet.** This repository is being built from the bottom up, and each piece is
written so it can be proved on a machine with no panel and no pad before it ever reaches
the hardware. What is here today is the part that reads the card.

## The hardware it is for

- Raspberry Pi 5 now, Raspberry Pi Zero 2 W next: both are 64-bit, so one build serves them.
- A 320×240 SPI panel with an ILI9341 controller, refreshed 20 times a second.
- A wired USB gamepad (a Rii GP100), read as ordinary Linux input.

## A game on the card

A game is a folder under `games/` holding its executable, its assets, the engine's
`veduta.json`, and a `card.json`:

```json
{
  "veduta": "card/1",
  "title": "Cave of Gems",
  "name": "gems",
  "version": "v1.2.0",
  "exec": "game",
  "icon": "icon.png"
}
```

`card.json` is deliberately **not** the engine's manifest. The engine's manifest is strict
and gains fields as the engine grows, so a console built today could not read one written
by a newer engine. This description never changes: `card/1` is frozen, and a console will
still list a game written years from now.

Nothing here is required. A folder with no description, or with a damaged one, is still
listed under its folder name and still launched if something in it can run — a console
never hides a game it could play. What it cannot do is launch a folder with no program in
it, and such a folder is passed over rather than shown as broken.

`exec` and `icon` name files inside the folder and nothing else: a card comes from a card,
and a card comes from anywhere. Without `exec`, the build for the board's own architecture
(`game-arm64`) is preferred, then a plain `game`, so one card can serve two boards.

## Building and testing

Everything here is standard-library Go, built without cgo, and the tests need no hardware:

```sh
go test ./...
```

## Licence

MIT.
