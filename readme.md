# experimental i3status cli in go

# screenshot

- C stand for CPU, M stand for Memory, S stand for Swap
  Full stand for Full Disk Total, Disk Used stand for Disk Used Percentage
  W stand for Wifi name, S stand for net speed(I guess, I don't know how to show net speed)
  ![screenshot](./screenshot/preview.png)

# setup for i3 config in ~/.config/i3/config

```bash
bar {
    #status_command i3status # original i3status program
    # NOTE: path to your i3statusgo binary
    status_command /home/etcix/code/gotest/i3statusgo/i3statusgo
    position bottom
    # NOTE: since I output so many thing and mark text of them, maybe the font size should not be too large
    font pango:DejaVu Sans Mono 12
}
```
