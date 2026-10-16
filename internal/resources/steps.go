package resources

import (
	"context"
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"k8s.io/client-go/tools/remotecommand"
)

// Step is what a command leads to; the tui shows it the same way for every
// kind: a *Picker, a *Viewer, an *Exec, a Cmd, or Stay.
type Step interface{ step() }

// Picker asks to choose one row, e.g. a container or a port. When Pick
// returns another screen, the picker stays underneath it (esc comes back);
// otherwise it closes.
type Picker struct {
	Title   string
	Columns []Column
	Rows    []Row
	Current string // key of the row to start on
	Hint    string // what enter does, e.g. "logs"
	Empty   string // shown when there are no rows
	// Load, if set, fetches the rows instead (in the background), again
	// every Every while the picker is open (0 = once).
	Load  func(ctx context.Context) ([]Row, error)
	Every time.Duration
	// Live, if set, adds rows computed from what k7s already knows, fresh on
	// every frame (after the loaded ones).
	Live func() []Row
	Pick func(key string) Step
}

// Stay keeps a picker open after a pick, e.g. after toggling an option.
type Stay struct{}

// Viewer shows text: streamed (logs) or fetched once (a secret).
type Viewer struct {
	Title string
	// Live streams keep the newest line in view until scrolled.
	Live bool
	// Stream emits lines until it returns (the stream ended) or ctx is done.
	Stream func(ctx context.Context, emit func(Line)) error
}

// Line is one line of a Viewer; Text may span several lines.
type Line struct {
	Text   string
	Tone   Tone
	Indent int  // of wrapped and further lines
	Secret bool // masked until revealed with r
}

// Exec hands the terminal to an interactive program, e.g. a shell.
type Exec struct {
	Banner string // printed before it starts
	Run    func(stdin io.Reader, stdout io.Writer, sizes remotecommand.TerminalSizeQueue) error
}

// Cmd is a step that just runs a command (and closes a picker).
type Cmd tea.Cmd

func (*Picker) step() {}
func (*Viewer) step() {}
func (*Exec) step()   {}
func (Cmd) step()     {}
func (Stay) step()    {}
