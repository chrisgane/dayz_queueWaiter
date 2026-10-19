# DayZ Queue Waiter

Keeps trying to join a full DayZ server for you. It works in DayZ's own
in-game server browser:

1. It double-clicks the server.
2. When the **CONNECTING FAILED — Maximum number of players has been
   reached** popup appears, it presses Enter.
3. It waits a random 1 to 2.5 seconds and goes again.
4. As soon as DayZ leaves the server browser (you're connecting, or in the
   queue), it stops and plays a tune.

It gives up after 30 minutes.

## Download

Open the latest successful run on the
[Actions tab](https://github.com/chrisgane/dayz_queueWaiter/actions), and
download **queuewaiter-windows** at the bottom. Unzip it to get
`queuewaiter.exe`. `SHA256.txt` holds its checksum; to check it in
PowerShell, run `Get-FileHash .\queuewaiter.exe`.

## Use

1. Start `queuewaiter.exe`. A small console window opens and stays open,
   and you hear a short beep (so you know sound works).
2. Start DayZ and open the server browser.
3. Hover the mouse over the server (don't click), then press **F8**. You
   hear two rising beeps when it starts.
4. Leave the mouse alone. It double-clicks the server, presses Enter on
   the "server full" popup, and repeats.
5. When you get in, you hear a tune three times and the window says
   **You're in!**

If F8 does nothing, click into the app's window and press **Enter**
instead. It beeps a 5-second countdown: switch to DayZ (Alt+Tab) and hover
over the server before it ends.

To stop at any time, press **F8** again (or Enter in the app's window) or
move the mouse. Switching away
from DayZ (Alt+Tab) pauses it: nothing is clicked or pressed unless DayZ is
the active window.

The window also shows the server's player count and queue (for example
`70/70 players, 5 in queue`). When that count drops, a place may have
opened, so the next try comes straight away instead of after the usual
pause.

### Sounds

| Sound | Meaning |
|---|---|
| Two rising beeps | Started |
| Two falling beeps | Stopped (F8 or mouse moved) |
| Rising tune, three times | You're in |
| Four falling notes | Gave up after 30 minutes |
| One low buzz | Couldn't start, or something went wrong. The window says why. |

## Settings

The defaults are for *Spaggies Vanilla Nasdara - Hosted by Jack*
(54.38.209.2:2302). To change them, run it from a command prompt or a
shortcut with options:

```
queuewaiter.exe -server 1.2.3.4:2302 -min 1s -max 2.5s -giveup 30m
```

| Option | Default | Meaning |
|---|---|---|
| `-server` | `54.38.209.2:2302` | The IP:port shown in the server browser's details panel |
| `-min`, `-max` | `1s`, `2.5s` | Shortest and longest pause between tries |
| `-giveup` | `30m` | How long to keep trying |
| `-query` | ask Steam | The server's query port, if Steam doesn't know it |

## Fullscreen

The app reads the screen to tell the server browser, the popup, and the
loading/queue screen apart. It uses the same screen capture as OBS's
display capture, which works with most fullscreen games. If it can't see
DayZ, pressing F8 gives a low buzz and the window says the screen reads as
black. In that case, set **Display mode** to **Borderless** in DayZ's video
options.

It expects a 16:9 resolution (1920×1080, 2560×1440, 3840×2160, and so on).

## Troubleshooting

Everything the app's window shows is also saved to `queuewaiter-log.txt`,
next to the exe, starting fresh each time the app opens. The first line
says which build you're running.

Press **F7** at any time to save what the app sees to
`queuewaiter-screenshot.png`, next to the exe. The window also shows what
the app thinks is on screen. If it gets something wrong, send that
screenshot so the detection can be adjusted.

How it recognises each screen:

- **Server browser:** the black bars above and below the server list.
- **Server full popup:** the red OK button on a grey panel in the middle of
  the screen, with a long line of text above it.
- **Anything else** (connecting, queue, loading, in game): no server
  browser. If this lasts 3 seconds, it counts as getting in.

If some other popup appears, it waits 3 seconds and then presses Enter to
close it.

## Note on BattlEye

The app sends ordinary mouse clicks and key presses to DayZ through
Windows, and only while you're in the menus. It doesn't touch the game's
files or memory. Even so, there's no guarantee that BattlEye or a server's
admins will approve of automated input. Use it at your own risk.

## Building

```
GOOS=windows GOARCH=amd64 go build -o queuewaiter.exe ./cmd/queuewaiter
go test ./...
```

The tests check screen detection against real DayZ screenshots in
`internal/detect/testdata`, at several resolutions. They also run the
retry logic against a simulated game.
