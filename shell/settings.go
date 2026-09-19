package shell

import (
	"strings"

	"github.com/riftbane/veduta/v2/gmath"
	"github.com/riftbane/veduta/v2/sim"
	"github.com/riftbane/veduta/v2/sprite"
	"github.com/riftbane/vedutaos/wifi"
)

// The settings, the Wi-Fi networks and the password keyboard: the screens behind the
// Settings row of the list.

func back(in sim.Input) bool {
	return in.JustPressed(sim.ButtonB) || in.JustPressed(sim.ButtonCancel)
}

func stepSettings(s State, in sim.Input) (State, Action) {
	n := len(SettingsItems)
	switch {
	case back(in):
		s.Screen = Games
	case in.JustPressed(sim.ButtonDown):
		s.SetSel = (s.SetSel + 1) % n
	case in.JustPressed(sim.ButtonUp):
		s.SetSel = (s.SetSel + n - 1) % n
	case in.JustPressed(sim.ButtonA):
		switch SettingsItems[s.SetSel] {
		case "WI-FI":
			s.Screen, s.NetSel, s.Notice = WiFi, 0, ""
			return s, Scan
		}
	}
	return s, Stay
}

func stepWiFi(s State, in sim.Input) (State, Action) {
	nets := s.WiFi.Networks
	if s.NetSel >= len(nets) {
		s.NetSel = max(0, len(nets)-1)
	}
	switch {
	case back(in):
		s.Screen, s.Notice = Settings, ""
	case in.JustPressed(sim.ButtonSelect):
		s.Notice = ""
		return s, Scan
	case len(nets) == 0:
	case in.JustPressed(sim.ButtonDown):
		s.NetSel, s.Notice = (s.NetSel+1)%len(nets), ""
	case in.JustPressed(sim.ButtonUp):
		s.NetSel, s.Notice = (s.NetSel+len(nets)-1)%len(nets), ""
	case in.JustPressed(sim.ButtonA):
		n := nets[s.NetSel]
		s.Target, s.Notice = n, ""
		switch {
		case n.Security == wifi.Unsupported:
			s.Notice = "THIS KIND OF NETWORK IS NOT SUPPORTED"
		case n.Security == wifi.Open || n.Saved:
			s.Keys = Keyboard{}
			return s, Join
		default:
			s.Screen, s.Keys = Password, Keyboard{}
		}
	}
	return s, Stay
}

// Keyboard is the password being typed on the on-screen keyboard, and where its cursor is.
type Keyboard struct {
	Text     string
	Row, Col int
	Page     int // keyPages: small letters, capitals, symbols
}

// keyPages are the keyboard's characters, a page at a time, row by row. Every character a
// WPA password can hold (printable ASCII) is on one of them.
var keyPages = [3][4]string{
	{"1234567890", "qwertyuiop", "asdfghjkl@", "zxcvbnm_.-"},
	{"1234567890", "QWERTYUIOP", "ASDFGHJKL@", "ZXCVBNM_.-"},
	{`!"#$%&'()*`, "+,/:;<=>?[", "\\]^`{|}~", "@_.- "},
}

// keyActions are the bottom row of the keyboard.
var keyActions = []string{"SHIFT", "#+=", "SPACE", "DEL", "JOIN"}

const actionRow = 4 // the row of keyActions, below the four rows of characters

func (k Keyboard) rowLen(row int) int {
	if row == actionRow {
		return len(keyActions)
	}
	return len(keyPages[k.Page][row])
}

func stepPassword(s State, in sim.Input) (State, Action) {
	k := &s.Keys
	switch {
	case in.JustPressed(sim.ButtonCancel):
		s.Screen, s.Notice = WiFi, ""
	case in.JustPressed(sim.ButtonB):
		if k.Text == "" {
			s.Screen, s.Notice = WiFi, ""
			break
		}
		k.Text, s.Notice = k.Text[:len(k.Text)-1], ""
	case in.JustPressed(sim.ButtonSelect):
		return join(s)
	case in.JustPressed(sim.ButtonLeft):
		k.Col = (k.Col + k.rowLen(k.Row) - 1) % k.rowLen(k.Row)
	case in.JustPressed(sim.ButtonRight):
		k.Col = (k.Col + 1) % k.rowLen(k.Row)
	case in.JustPressed(sim.ButtonUp):
		k.moveRow(-1)
	case in.JustPressed(sim.ButtonDown):
		k.moveRow(1)
	case in.JustPressed(sim.ButtonA):
		if k.Row < actionRow {
			if len(k.Text) < 63 {
				k.Text += string(keyPages[k.Page][k.Row][k.Col])
			}
			s.Notice = ""
			break
		}
		switch keyActions[k.Col] {
		case "SHIFT":
			k.Page = map[int]int{0: 1, 1: 0, 2: 0}[k.Page]
		case "#+=":
			k.Page = map[int]int{0: 2, 1: 2, 2: 0}[k.Page]
		case "SPACE":
			if len(k.Text) < 63 {
				k.Text += " "
			}
		case "DEL":
			if k.Text != "" {
				k.Text = k.Text[:len(k.Text)-1]
			}
		case "JOIN":
			return join(s)
		}
		s.Notice = ""
	}
	return s, Stay
}

// moveRow moves the cursor a row up or down, keeping it under the same place: the action
// row's keys are two characters wide.
func (k *Keyboard) moveRow(d int) {
	from := k.Row
	k.Row = (k.Row + d + actionRow + 1) % (actionRow + 1)
	switch {
	case from == actionRow:
		k.Col *= 2
	case k.Row == actionRow:
		k.Col /= 2
	}
	k.Col = min(k.Col, k.rowLen(k.Row)-1)
}

func join(s State) (State, Action) {
	if !wifi.PassphraseOK(s.Keys.Text) {
		s.Notice = "A PASSWORD HAS 8 TO 63 CHARACTERS"
		return s, Stay
	}
	s.Screen, s.Notice = WiFi, ""
	return s, Join
}

// drawer holds the measures every screen is drawn with.
type drawer struct {
	b                                 *sprite.Batch
	r                                 Resources
	w, h, scale, cell, pad, rowH, top int
}

func newDrawer(b *sprite.Batch, r Resources, w, h int) *drawer {
	scale := max(1, h/240)
	d := &drawer{b: b, r: r, w: w, h: h, scale: scale, cell: 8 * scale, pad: 4 * scale}
	d.rowH = d.cell + 2*scale
	d.top = d.pad + d.cell + d.pad
	return d
}

// rows is how many rows of a list fit between the header and the footer, with a clear gap
// above the footer: on a small panel a list that touches it reads as one block of text.
func (d *drawer) rows() int {
	return max(1, (d.h-d.top-2*d.pad-d.cell)/d.rowH)
}

func (d *drawer) text(x, y int, s string, colour uint32) {
	d.b.Text(d.r.Font, d.r.FontTex, float32(x), float32(y), d.scale, s, colour)
}

// right draws s ending at x.
func (d *drawer) right(x, y int, s string, colour uint32) {
	d.text(x-len([]rune(s))*d.cell, y, s, colour)
}

func (d *drawer) rect(x, y, w, h int, colour uint32) {
	d.b.Rect(gmath.R(float32(x), float32(y), float32(w), float32(h)), colour)
}

func (d *drawer) header(title, right string) {
	d.text(d.pad, d.pad, title, colHeader)
	if right != "" {
		d.right(d.w-d.pad, d.pad, right, colDim)
	}
}

func (d *drawer) footer(s string, colour uint32) {
	d.text(d.pad, d.h-d.pad-d.cell, clip(s, (d.w-2*d.pad)/d.cell), colour)
}

// highlight marks the selected row whose text is at y.
func (d *drawer) highlight(y int) {
	d.rect(d.pad/2, y-d.scale, d.w-d.pad, d.rowH, colSelected)
}

// gear is the Settings row's tile: a square with a hole, drawn rather than loaded.
func (d *drawer) gear(x, y, side int) {
	d.rect(x, y, side, side, colPlaceholder)
	u := max(1, side/8)
	d.rect(x+3*u, y+u, 2*u, side-2*u, colHeader)
	d.rect(x+u, y+3*u, side-2*u, 2*u, colHeader)
	d.rect(x+3*u, y+3*u, 2*u, 2*u, colPlaceholder)
}

func (d *drawer) settings(s State) {
	d.header("SETTINGS", "")
	for i, item := range SettingsItems {
		y := d.top + i*d.rowH
		if i == s.SetSel {
			d.highlight(y)
		}
		d.text(d.pad, y+d.scale, item, colText)
		value := ""
		switch item {
		case "WI-FI":
			value = wifiSummary(s.WiFi)
		}
		room := (d.w-2*d.pad)/d.cell - len(item) - 2
		d.right(d.w-d.pad, y+d.scale, clip(value, room), colDim)
	}
	if s.Version != "" {
		d.text(d.pad, d.h-d.pad-3*d.cell, "VEDUTAOS "+s.Version, colDim)
	}
	footer := "A: OPEN   B: BACK"
	if s.Notice != "" {
		footer = s.Notice
	}
	d.footer(footer, colNotice)
}

// wifiSummary is the Wi-Fi's state in a few words, beside its row in the settings.
func wifiSummary(st wifi.Status) string {
	switch st.State {
	case wifi.NoAdapter:
		return "NONE"
	case wifi.Starting:
		return "STARTING"
	case wifi.Connecting:
		return "JOINING"
	case wifi.Connected:
		return st.SSID
	}
	return "NOT CONNECTED"
}

// wifiLine is the Wi-Fi's state on the Wi-Fi screen, and whether it is bad news.
func wifiLine(st wifi.Status) (string, bool) {
	switch st.State {
	case wifi.NoAdapter:
		return "NO WI-FI ON THIS CONSOLE", true
	case wifi.Starting:
		return "STARTING WI-FI", false
	case wifi.Connecting:
		return "JOINING " + st.SSID, false
	case wifi.Connected:
		if st.IP == "" {
			return "ON " + st.SSID + ", GETTING AN ADDRESS", false
		}
		return "ON " + st.SSID + " " + st.IP, false
	case wifi.WrongPassword:
		return "WRONG PASSWORD FOR " + st.SSID, true
	case wifi.Failed:
		return "COULD NOT JOIN " + st.SSID, true
	}
	return "NOT CONNECTED", false
}

func (d *drawer) wifi(s State) {
	right := ""
	if s.WiFi.Scanning {
		right = "SEARCHING"
	}
	d.header("WI-FI", right)
	line, bad := wifiLine(s.WiFi)
	colour := colText
	if bad {
		colour = colNotice
	}
	d.text(d.pad, d.top, clip(line, (d.w-2*d.pad)/d.cell), colour)

	top := d.top + d.rowH + d.pad
	rows := max(1, (d.h-top-2*d.pad-d.cell)/d.rowH)
	nets := s.WiFi.Networks
	sel := min(s.NetSel, max(0, len(nets)-1))
	first := 0
	if sel >= rows {
		first = sel - rows + 1
	}
	barsW := 4 * 2 * d.scale
	for i := 0; i < rows && first+i < len(nets); i++ {
		n := nets[first+i]
		y := top + i*d.rowH
		if first+i == sel {
			d.highlight(y)
		}
		d.bars(d.pad, y, n.Bars())
		tag, tagColour := "", colDim
		switch {
		case s.WiFi.State == wifi.Connected && n.SSID == s.WiFi.SSID:
			tag, tagColour = "ON", colHeader
		case n.Saved:
			tag = "SAVED"
		case n.Security == wifi.Unsupported:
			tag = "N/A"
		}
		x := d.pad + barsW + d.pad
		room := (d.w - x - d.pad) / d.cell
		if tag != "" {
			room -= len(tag) + 1
		}
		if n.Security == wifi.PSK {
			room -= 2
			d.lock(d.w-d.pad-len(tag)*d.cell-d.cell-2*d.scale, y)
		}
		name := printable(n.SSID)
		colour := colText
		if n.Security == wifi.Unsupported {
			colour = colDim
		}
		d.text(x, y+d.scale, clip(name, room), colour)
		if tag != "" {
			d.right(d.w-d.pad, y+d.scale, tag, tagColour)
		}
	}
	if len(nets) == 0 && s.WiFi.State != wifi.NoAdapter {
		msg := "NO NETWORKS FOUND"
		if s.WiFi.Scanning || s.WiFi.State == wifi.Starting {
			msg = "SEARCHING"
		}
		d.text((d.w-len(msg)*d.cell)/2, d.h/2-d.cell/2, msg, colDim)
	}
	footer := "A: JOIN   B: BACK   START: SEARCH"
	if s.Notice != "" {
		footer = s.Notice
	}
	d.footer(footer, colNotice)
}

// bars draws a network's signal: four bars, rising, lit up to n.
func (d *drawer) bars(x, y, n int) {
	w := d.scale
	for i := 0; i < 4; i++ {
		bh := (i + 1) * (d.cell - d.scale) / 4
		colour := colPlaceholder
		if i < n {
			colour = colText
		}
		d.rect(x+i*2*w, y+d.cell-d.scale-bh, w, bh, colour)
	}
}

// lock marks a network that takes a password.
func (d *drawer) lock(x, y int) {
	u := d.scale
	d.rect(x+u, y+u, 3*u, u, colDim)
	d.rect(x+u, y+u, u, 3*u, colDim)
	d.rect(x+3*u, y+u, u, 3*u, colDim)
	d.rect(x, y+3*u, 5*u, 4*u, colDim)
}

// printable is a network's name as the font can draw it: characters it has no glyph for
// become '?'.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r > 255 || r == 127 {
			return '?'
		}
		return r
	}, s)
}

func (d *drawer) password(s State) {
	d.header("PASSWORD", "")
	d.text(d.pad, d.top, clip("FOR "+printable(s.Target.SSID), (d.w-2*d.pad)/d.cell), colDim)

	// The field: what was typed, its end when it is longer than the field, and a cursor.
	fy := d.top + d.rowH + d.pad
	d.rect(d.pad, fy, d.w-2*d.pad, d.rowH+2*d.scale, colSelected)
	room := (d.w-4*d.pad)/d.cell - 1
	shown := s.Keys.Text
	if len(shown) > room {
		shown = shown[len(shown)-room:]
	}
	d.text(2*d.pad, fy+2*d.scale, shown+"_", colText)

	// The keys: ten a row, the action row's keys twice as wide.
	keyW := (d.w - 2*d.pad) / 10
	keyH := d.rowH + 4*d.scale
	ky := fy + d.rowH + 2*d.pad + 2*d.scale
	x0 := (d.w - 10*keyW) / 2
	k := s.Keys
	for row := 0; row <= actionRow; row++ {
		y := ky + row*keyH
		for col := 0; col < k.rowLen(row); col++ {
			label, x, w := "", x0+col*keyW, keyW
			if row == actionRow {
				label, x, w = keyActions[col], x0+col*2*keyW, 2*keyW
			} else {
				label = string(keyPages[k.Page][row][col])
			}
			bg := colPlaceholder
			if row == k.Row && col == k.Col {
				bg = colHeader
			} else if row == actionRow && ((label == "SHIFT" && k.Page == 1) || (label == "#+=" && k.Page == 2)) {
				bg = colSelected
			}
			d.rect(x+d.scale, y, w-2*d.scale, keyH-2*d.scale, bg)
			if label == " " {
				label = "SP"
			}
			tc := colText
			if row == k.Row && col == k.Col {
				tc = colBackground
			}
			d.text(x+(w-len(label)*d.cell)/2, y+(keyH-2*d.scale-d.cell)/2, label, tc)
		}
	}
	footer := "A: TYPE   B: DELETE   START: JOIN"
	if s.Notice != "" {
		footer = s.Notice
	}
	d.footer(footer, colNotice)
}
