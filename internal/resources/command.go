package resources

// Command is something the selected row can do: show logs, open a shell,
// describe it. Kind-specific commands live in the kind's commands folder
// (resources/pods/commands/shell.go), commands for every kind in
// resources/commands. The config decides which commands a section shows and
// on which keys; the others exist but stay unbound.
type Command struct {
	// Name is how the config refers to it, e.g. "shell".
	Name string
	// Run does it for the selected row; it may act directly and return nil.
	Run func(t Target) Step
}

// Target is the row a command runs on.
type Target struct {
	Host Host
	Kind Resource // the kind showing the row, e.g. *pods.Resource
	Key  string   // the row's key in that kind
	Ref  Ref      // the object it shows
}
