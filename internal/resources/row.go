package resources

import (
	"fmt"
	"time"
)

// What a table shows. Kinds fill these in; the tui lays them out (widths,
// truncation, colors, the # column, selection).

// Column describes one table column.
type Column struct {
	Title  string
	Right  bool // right-aligned (numbers)
	Middle bool // shorten as "prefix…suffix", keeping e.g. a pod hash
	Shrink bool // gives up width first when the terminal is too narrow
	Drop   int  // > 0: hidden when still too narrow, lowest first
}

// Row is one table row.
type Row struct {
	Key    string
	Ref    Ref    // the object it shows, for following links
	State  string // one of the kind's States, so the user can hide it
	Cells  []Cell // one per column
	Struck bool   // deleted, lingering for a moment
}

// Cell is one table cell, or a part of a status or change line.
type Cell struct {
	Text string
	Tone Tone
}

// Tone is what a cell means; the tui picks the color.
type Tone int

const (
	Plain  Tone = iota
	Dim         // secondary information
	Good        // green: running, synced, healthy
	Busy        // amber: on its way (pending, progressing)
	Warn        // yellow: worth a look (restarts, high usage)
	Bad         // red
	Alarm       // bold red: failing
	Info        // cyan
	Flash       // just changed
	Fresh       // just added
	Strong      // bold
)

// FlashFor is how long a changed cell stays highlighted.
const FlashFor = 3 * time.Second

// Lights is the usual "lights" status: how many rows, and how many of them
// are green, amber and red ("8 ●5 ●2 ●1"); nothing for no rows.
func Lights(tones []Tone) []Cell {
	if len(tones) == 0 {
		return nil
	}
	var good, busy, bad int
	for _, t := range tones {
		switch t {
		case Good:
			good++
		case Busy, Warn:
			busy++
		case Bad, Alarm:
			bad++
		}
	}
	return []Cell{
		{Text: fmt.Sprintf("%d ", len(tones))},
		{Text: fmt.Sprintf("●%d ", good), Tone: Good},
		{Text: fmt.Sprintf("●%d ", busy), Tone: Busy},
		{Text: fmt.Sprintf("●%d", bad), Tone: Bad},
	}
}

// Light is the tone of a traffic light: no light, green, amber, red.
func Light(level int) Tone {
	switch level {
	case 1:
		return Good
	case 2:
		return Busy
	case 3:
		return Alarm
	}
	return Dim
}
