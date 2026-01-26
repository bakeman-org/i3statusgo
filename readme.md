# experimental i3status cli in go

# setup for i3 config in ~/.config/i3/config

```bash
bar {
    #status_command i3status # original i3status program
    # path to your i3statusgo binary
    status_command /home/etcix/code/gotest/i3statusgo/i3statusgo
    position bottom
    # since I output so many thing and mark text of them, maybe the font size should not be too large
    font pango:DejaVu Sans Mono 12
}
```

# todo

1. [v] nmcli get wifi name fail, output: W: ( 0% at disconnected)
   but actual wifi name can get from, seems fix already?

```bash
$ nmcli -t -f "NAME,TYPE" connection show --active
CMCC-Z2xF:802-11-wireless
lo:loopback
```
